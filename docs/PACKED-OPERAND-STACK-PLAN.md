# Packed-operand-stack migration plan

> **Status: x86-64 done, arm64 outstanding** — tracked as
> [#4111](https://github.com/JakeChampion/lang/issues/4111) (8-byte stack
> slots + real push/pop on the native backends).

Captures the multi-PR migration strategy for BACKEND-PARITY
perf item #3 (Pack the operand-stack into 8-byte slots).
Same shape as the SSO plan — the change is mechanical but
the surface area is large enough that landing it as a single
PR carries unacceptable risk of subtle offset-arithmetic bugs.

## Status today

x86-64 uses **8-byte slots** with real `push` / `pop`. arm64
still pushes a **16-byte slot** per value (`str x0, [sp, #-16]!`)
regardless of value width — the 16-byte slot was an alignment
hedge for `stp` / `ldp`, and AAPCS64 wants sp 16-aligned for any
sp-relative access, not only at calls, so the arm64 flip is not
the same change x86-64's was.

`internal/codegen/arm64/arm64.go` has ~200 references to `#16`
(mostly slot arithmetic, some unrelated bit masks).

## Target

8-byte operand-stack slots on both natives. Halves operand-
stack memory; tighter stack frames mean smaller working-set;
fewer cache misses on deep call chains. Re-align sp to 16 at
call boundaries (already done at the stack-arg overflow pad —
extend to ALL call boundaries).

Same `(data, len)` style as SSO: per-slot encoding stays one
value per slot. The win is purely from halving the slot size.

## Migration steps

### Step 1 — introduce a `slotBytes` constant (this PR slot)

Per-backend `const slotBytes = 16` in each native codegen
file, with every `rsp, 16` / `#16` literal that means "one
operand-stack slot" rewritten to reference it. Mechanical
search-and-replace; no behavior change. Establishes the
single point of truth.

Verification: full `go test ./...` green, no asm diff (the
constant inlines to the same literal).

### Step 2 — flip `slotBytes` to 8 on x86_64 ✅ done

`slotBytes = 8`; `push()` / `pop()` emit `push rax` / `pop DST`;
`binPop` / `fbinPop` / the inline index helper / the closure
env builder pop registers directly. Parity is tracked in
`generator.opBytes` — every body-level rsp movement goes through
`pushReg` / `popReg` / `rspAlloc` / `rspFree`, and `irScope`
carries the scope-entry depth so an else-branch does not inherit
the then-branch's pushes. `callAligned` pads 8 bytes before a
call when the depth is odd; `emitCallArgsLoad` folds the same
pad into the stack-argument overflow area, because a pad emitted
after it would move the outgoing arguments out from under
`[rsp]`.

The peephole moved with it: P1 is now `push rax` / `pop DST` =>
`mov DST, rax`, P3 is `push rax` / `add rsp, 8` => nothing.

Verification: `TestEmittedCallsAre16ByteAligned` re-derives rsp
from the emitted text (not from the counter that produced it)
and reports any call reached at an odd multiple of 8, over a
shape corpus and all of `examples/`. Plus the native fixture
suite with and without `FERN_NATIVE_ASM=1`, and the differential
oracle.

Measured on `examples/self_host/fern.fern` for `x86-64-linux`:
executable segment 22,206,407 -> 17,692,055 bytes (-20.3%).
Whole-binary is only -5.0%, because ~79% of that artifact is a
64 MiB zero-fill `.bss` reservation the linker materialises in
the file rather than machine code.

### Step 3 — 8-byte slots on arm64

NOT the same change as step 2. AAPCS64 requires a 16-byte-aligned
sp for every sp-relative access, so unaligned 8-byte pushes are
not merely a parity-tracking problem the way they are on x86-64.
The realistic shape is pairing two 8-byte spills into one
`stp` / `ldp` rather than halving the slot.

### Step 4 — verify across all callers

Stress tests on deep call chains, closures, defers, the
prelude routes that pass through many helpers (HTTP request
parsing, JSON encode/decode).

## Why incremental

`slotBytes` constant introduction has ZERO risk (no value
change). The flip per backend isolates blast radius if a bug
slips through. The "verify" step is a separate PR with
nothing but added tests + benchmarks.

## Why packed slots, not register-pinning

Eliminating the operand-stack-as-`sp` model entirely (the
"real" register-allocating codegen) is a bigger redesign that
breaks the IR/codegen separation. Slot packing is the
smaller-blast-radius improvement that keeps the existing
architecture.

## Estimated PR count

4 PRs. Each is independently mergeable. Steps 1 and 2 have
landed.

## Why this works even though SSO doesn't

The operand stack is internal to each function — no
cross-function ABI break. Once `slotBytes` is consistent
within a function, callers/callees still pass args in
registers per SysV/AAPCS64. SSO breaks because string args
sit IN the calling convention; operand-stack slots don't.

https://claude.ai/code/session_01LXybxbbVBbwLFHmbYAobhN
