# Packed-operand-stack migration plan

Captures the multi-PR migration strategy for BACKEND-PARITY
perf item #3 (Pack the operand-stack into 8-byte slots).
Same shape as the SSO plan — the change is mechanical but
the surface area is large enough that landing it as a single
PR carries unacceptable risk of subtle offset-arithmetic bugs.

## Status today

Every operand-stack push uses a **16-byte slot** regardless
of value width on both natives. The 16-byte slot was a
16-byte-alignment hedge for `stp` / `ldp` on arm64 and SysV's
pre-call alignment.

Hard-coded literal `16`s in the native codegens:

- `internal/codegen/x86_64/x86_64.go`: ~80 references to
  `rsp, 16` / `[rsp + 16*N]` / `[rsp + 16]` shapes.
- `internal/codegen/arm64/arm64.go`: ~200 references to
  `#16` (mostly slot arithmetic, some unrelated bit masks).

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

### Step 2 — flip `slotBytes` to 8 on one backend (x86_64 first)

Change the constant on x86_64 only. Audit + fix every
offset-calculation that assumed 16-byte slots. Add the static
parity-tracking that pads sp to 16 before every call site.

Verification: full e2e suite green on x86_64; arm64 + wasm
unchanged.

### Step 3 — flip `slotBytes` to 8 on arm64

Same as step 2 for arm64.

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

4 PRs. Each is independently mergeable.

## Why this works even though SSO doesn't

The operand stack is internal to each function — no
cross-function ABI break. Once `slotBytes` is consistent
within a function, callers/callees still pass args in
registers per SysV/AAPCS64. SSO breaks because string args
sit IN the calling convention; operand-stack slots don't.

https://claude.ai/code/session_01LXybxbbVBbwLFHmbYAobhN
