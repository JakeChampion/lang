# Self-hosted compiler: SSA always-on, every backend on the IR

## Goal

Make the self-hosted Fern compiler (`examples/self_host/`) compile
**through the SSA IR by default**, with **every backend consuming that
IR** — so the IR (`ssa.fern`) becomes the single lowering path the way
`internal/ir` is for the Go compiler. The AST emitters (`asm.fern`,
`asm_arm64.fern`, `wasm.fern`) are retired once SSA reaches parity.

This is the self-hosted mirror of the project's "the IR layer is
target-agnostic; new optimisations live in the IR so all backends
benefit" principle (CLAUDE.md / `docs/BACKEND-PARITY.md`).

## Where we started

The self-hosted pipeline had **two coexisting paths**:

```
                        ┌─ SSA path (opt-in: -ssa, x86-64/arm64 only) ─┐
lex → parse → flatten → check ─┤                                       ├─→ asm
                        └─ AST path (default, all targets) ────────────┘
```

- **SSA IR** (`ssa.fern`): `SInst` / `STerm` / `SBlock` / `SFunc`, plus a
  builder (`build_func`), an optimiser (copy-prop, const-fold, CSE, DCE,
  branch-simplify, block-merge), and a linear-scan register allocator.
  The subset `build_func` handles is already broad: i32 arithmetic,
  control flow, calls + recursion, heap arrays, strings (`concat` /
  `streq`), structs, tuples, lambdas, capturing closures, receiver
  methods, i32/string maps, and enums + `match`.
- **SSA consumers**: `ssa_x86.fern`, `ssa_arm64.fern`.
- **No SSA consumer**: wasm (`wasm.fern` always emits from the AST).
- **Driver** (`fern.fern`): `-ssa` opt-in, attempted only for x86-64 /
  arm64, transparent fallback to the AST emitter when `build_func`
  returns `ok=false` (floats, generics, struct spread, a few patterns)
  or for any other target.

`build_func`'s fallback points (the `s.fail()` sites) are the remaining
gaps: floats (`ExprNumber.is_float`), generics, `...base` struct spread,
tagged `break`/`continue`, non-variant `match` patterns, and the
catch-all `_ => s.fail()` for unhandled expr/stmt kinds.

## Strategy: incremental, fallback-preserving

The AST emitters stay as a **correctness fallback** while SSA coverage
grows. We flip the default and add the missing wasm consumer first, then
close the coverage gaps, and only delete the AST emitters once SSA is at
full parity and the self-hosting fixpoint is SSA-clean. At no point does
the compiler emit worse or wrong code: anything SSA can't yet lower falls
back exactly as today.

## Phases

### Phase 1 — SSA on by default (x86-64 + arm64) ✅ this PR

- `fern.fern`: attempt the SSA pipeline on **every** compile for x86-64 /
  arm64 (no flag needed). Add `-no-ssa` to force the AST emitter. `-ssa`
  is still accepted (now a no-op) for back-compat.
- The transparent fallback is unchanged: out-of-subset programs still
  route to the AST emitter automatically, so output is never wrong.
- Tests: the in-subset programs in `self_host_cli_test.go` now compile
  through SSA by default; the SSA-vs-AST contrast tests use `-no-ssa` as
  the AST baseline, and a new case pins "default == `-ssa`, default !=
  `-no-ssa`" for an in-subset program.

This makes "always enable SSA" true for the two backends that already
consume the IR.

### Phase 2 — SSA → wasm backend (`ssa_wasm.fern`)

The missing third consumer. New backend `emit_program(funcs:
ssa.SFunc[]) : string` producing WAT, wired into `try_ssa` for
`-target wasm` (fall back to `wasm.fern` otherwise).

The one structural difference from x86/arm64: WAT has **no arbitrary
jumps**, so the SSA CFG is lowered with the standard *dispatch-loop*
shape — a `(block $exit (loop $L (block … (br_table $blk0 … (local.get
$pc)))))` over a `$pc` local that each terminator sets before branching
back to the dispatch. SSA values become wasm locals; phis become edge
copies (as on the native backends). On wasm32 every value (i32 and
pointer alike) is a 32-bit local, so the native backends' width
bookkeeping disappears here.

**Phase 2a ✅ (landed):** the core integer subset — `const_int`,
`const_bool`, `binary`, `unary`, `call`, `param`, `copy`, `phi` over
`ret` / `br` / `brif`. `ssa_wasm.supported()` lets `try_ssa` fall back to
`wasm.fern` for any program that needs an instruction this backend
doesn't lower yet, so output is never wrong — and `-target wasm` is now
default-on through the IR for in-subset programs. Gated by 30 differential
cases under `wasmtime` (`self_host_ssa_wasm_emit_test.go`, a subset of the
`ssa_emit_test` matrix) plus a CLI integration case (`emit-ssa-wasm`).

**Phase 2b ✅ (landed — heap batch):** `alloc` / `load_elem` /
`store_elem` over a linear-memory bump allocator (a `$__hp` global, 16 MiB
arena). On wasm32 every value is a 4-byte i32 word, so the native
backends' 8-byte stride / pointer width collapses to a uniform word. This
brings arrays, strings (index / len / param), structs (+ fields, params,
returns), methods, tuples, i32 maps, struct-union `match`, and the
runtime helpers built only from heap + call ops (array `push` / `slice`,
the i32 `__ssa_map_*` ops) onto the IR for wasm. Gated by ~30 added
differential cases. **Hardening:** making `try_ssa` run by default
(Phase 1) exposed a latent AST-mutation bug — `collect_lambdas` appended
`__env` to a lambda's params *in place* (`.push` mutates), corrupting the
shared AST that the fallback emitter then reused (a duplicate `$__env` →
invalid WAT). Fixed by copying the params first; guarded by an
`emit-ssa-wasm` regression case (default WAT == `-no-ssa` WAT for a
capturing-lambda fallback).

**Phase 2c ✅ (landed — strings):** `concat` / `streq` via small WAT
runtime helpers (string build + content equality over the `[len, c0, …]`
word layout). String-building programs now lower to the IR on wasm.

**Phase 2d ✅ (landed — print + the newline fix):** canonical Fern `print`
/ `eprint` append a newline (the native backend, the interpreter, and the
AST emitters all do), but the SSA backends did a raw write *without* it —
a latent bug the default-on switch (Phase 1) made user-visible (a
`print(x)` with no explicit `\n` dropped its newline on the x86-64 /
arm64 default path). Fixed by appending the newline in all three SSA
`print` helpers (`ssa_x86`, `ssa_arm64`, and the new `ssa_wasm`
`fd_write` path) and rewriting `self_host_ssa_print_test.go` to println
semantics. `print` now lowers to the IR on wasm too.

**Phase 2e (next):** the last reject-list entries — `funcaddr` /
`call_indirect` (escaping closures via a function table). Clearing them
makes **all three backends consume the IR** for the whole current SSA
subset.

(arm64-darwin reuses the arm64 SSA output with a Mach-O reskin —
`ssa_arm64` emit + a `darwinize`-equivalent on the SSA framing — folded
in here or in Phase 3.)

### Phase 3 — close the `build_func` coverage gaps

Each item deletes an `s.fail()` site and joins the SSA subset (with
matching cases in every backend's differential matrix):

- **Floats (f64)** — the largest item: a floating value class in the IR
  and an XMM/SSE path in x86-64, the FP register file in arm64, and the
  f64 ops in wasm. The IR currently `fail()`s on `is_float`.
- **Generics by erasure** in `build_func` (the AST emitters already do
  this).
- **`...base` struct spread**, remaining **`match` patterns**, **tagged
  `break`/`continue`**.

### Phase 4 — compile the compiler itself through SSA

Drive the self-hosting fixpoint (the compiler compiling its own sources)
through SSA rather than the AST fallback. Requires Phase 3, since the
compiler sources use floats and generics. Success = the fixpoint is
byte-stable with SSA as the lowering path on x86-64 and arm64.

### Phase 5 — retire the AST emitters

Once SSA is at full parity and the fixpoint is SSA-clean, remove the AST
lowering in `asm.fern` / `asm_arm64.fern` / `wasm.fern` (or reduce them
to thin SSA-backend shells), drop the fallback and `-no-ssa`. The IR is
then the single lowering path for every backend — the self-hosted mirror
of `internal/ir`.

## Engineering bar

Every phase ships as its own PR with x86-64 + CI-gated arm64 tests (and
wasm tests for the wasm backend), cross-checked against the AST emitter,
and must keep the self-hosting fixpoint byte-identical. New IR coverage
ships with differential cases at the layer it touches.
