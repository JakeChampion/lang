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

**Phase 2e ✅ (landed — closures):** `funcaddr` / `call_indirect` via a
wasm function table. The challenge is the SSA closure ABI: every indirect
call passes the closure box as a trailing `env` argument, so the call
site uses `$clos<U+1>` (U user args + env). A capturing lambda's lifted
body already takes `__env` as its last param, so its type matches; but a
no-capture function value has only `U` params, and wasm's `call_indirect`
is **strictly typed** (x86 / arm64 silently tolerate the unused extra arg
in a register — wasm does not). Solved by adding an `SFunc.takes_env` flag
(set when the last param is `__env`) and, in `ssa_wasm`, building a
function table whose slot for each function holds its *closure-callable*
form: the function itself if it takes `__env`, else an env-dropping
wrapper (`(param user…) (param env) → call $__fn_T user…`). `funcaddr` is
the table slot index; `call_indirect` dispatches through `$clos<arity>`.

With this, **all three backends consume the IR** for the whole current
SSA subset (integers, heap, strings, print, closures). `supported()` is
now effectively always true for `build_func` output, but stays as a
forward-compatibility guard so a future IR kind falls back rather than
emitting broken WAT.

(arm64-darwin reuses the arm64 SSA output with a Mach-O reskin —
`ssa_arm64` emit + a `darwinize`-equivalent on the SSA framing — folded
in here or in Phase 3.)

### Phase 3 — close the `build_func` coverage gaps

Each item deletes an `s.fail()` site and joins the SSA subset (with
matching cases in every backend's differential matrix):

- **Floats (f64)** — the largest item: a floating value class in the IR
  (`const_float`, the `compute_isfloat` pass) and an XMM/SSE path in
  x86-64, the FP register file in arm64, and the f64 ops in wasm.
  - **Phase 3a ✅ (landed — f64 in the IR + x86-64, intra-function):**
    `build_func` lowers float literals (`const_float`, carrying the source
    text), float arithmetic / negation / comparison (reusing `binary` /
    `unary`; `compute_isfloat` marks which values are f64), and `as f64` /
    `as i32` casts. `ssa_x86` emits them with SSE2 over 8-byte stack slots
    (`.rodata` `.double` constants, `movsd` / `addsd` / `ucomisd` /
    `cvtsi2sd` / `cvttsd2si`); float functions skip register allocation so
    every value is slot-addressable, and float values are 64-bit-wide so
    copy / phi-edge moves carry all 8 bytes. Also fixed a `const_fold` bug
    it surfaced (it folded `as_f64 k` as `!k`). Gated by
    `TestSelfHostSSAEmitX86_64` float cases.
  - **Phase 3b ✅ (landed — the x86-64 f64 call ABI):** float **params**
    arrive in xmm0…, float **returns** come back in xmm0, and mixed
    int/float args fill the two System V register sequences independently
    (`param str="f64"`; a `mark_float_calls` pass tags f64-returning calls
    `imm=2` so the result is read from xmm0). Float functions now cross
    call boundaries on x86-64 — params, returns, recursion, mixed args.
  - **Phase 3c-wasm ✅ (landed — wasm SSA floats):** `ssa_wasm` lowers f64
    to native wasm types — f64 locals / params / results, `f64.add` /
    `f64.lt` / `f64.neg` / `f64.convert_i32_s` / `i32.trunc_f64_s`
    (wasm is typed, so no width bookkeeping). `supported()` admits
    `const_float`, and the wasm emit driver runs `mark_float_calls`.
  - **Phase 3c-arm64 ✅ (landed — arm64 SSA floats):** `ssa_arm64` lowers
    f64 with the FP `d` registers — `.double` rodata via adrp/ldr, `fadd` /
    `fsub` / `fmul` / `fdiv`, `fcmp` + `cset` (the FP condition codes:
    `<`→mi so NaN is false), `fneg`, `scvtf` (i32→f64), `fcvtzs` (f64→i32),
    and the AAPCS64 call ABI (d0… params, d0 result). The arm64
    `ssa.any_float` fallback gate is removed.

  **f64 is now at parity across all three backends** (x86-64, arm64,
  wasm) — locals, arithmetic, comparison, casts, loop/if phis, and the
  full call ABI (params / returns / args / recursion). Gated by float
  cases in `TestSelfHostSSAEmit{X86_64,Arm64,Wasm}`.
- **Generics by erasure** in `build_func` (the AST emitters already do
  this).
- **`...base` struct spread**, remaining **`match` patterns**, **tagged
  `break`/`continue`**.

### Phase 4 — compile the compiler itself through SSA

Drive the self-hosting fixpoint (the compiler compiling its own sources)
through SSA rather than the AST fallback. Requires Phase 3, since the
compiler sources use floats and generics. Success = the fixpoint is
byte-stable with SSA as the lowering path on x86-64 and arm64.

Because `try_ssa` is all-or-nothing per program, *every* function the
compiler defines must be in the `build_func` subset before the whole
compiler self-compiles through SSA. The per-module coverage is now
effectively 100% — the holdouts surfaced while measuring it:

- **`const_str` for string literals** (landed, #2323) — fixed the
  per-function OOM from byte-by-byte literal lowering.
- **Open-ended slice `x[lo:]`** (this slice) — the self-host parser stored
  the omitted high bound as an `ExprUnknown` placeholder, which *every*
  backend mistranslated (asm / asm_arm64 push `$0`, so the slice length
  became `0 - lo`; build_func `s.fail()`d on the unknown). This was a
  latent **correctness bug** masked only because the full self-compile
  hadn't run — `cast_target`'s `op[3:]` (the sole open-ended slice in the
  sources) would have returned garbage. Fixed at the parser, mirroring the
  Go compiler's nil-`High` → length default: the omitted bound desugars to
  `base.len()`, so the checker, all three AST emitters, and SSA
  `build_func` uniformly see a real i32 end. Validated end-to-end on the
  self-host x86-64 CLI through both the SSA and `-no-ssa` (AST) paths
  (`"as_f64"[3:]` → `"f64"`). Gated by a parser self-test (desugar shape)
  and open-ended-slice cases in the x86-64 / arm64 / wasm SSA emit
  matrices.

#### Cumulative allocation (the no-GC wall)

With per-function coverage at ~100%, the remaining blocker is **memory**:
the self-host runtime is a no-GC bump allocator, so building every
function's SSA in one process accumulates all the dead per-function
intermediates. The AST path survives self-compile only because it reserves
a 1 GiB heap; the SSA emitter path was sized for small programs.

Reducing per-function allocation (the chosen first attack — measured with a
no-GC `ssa_emit_run` driver, whose zero-paged heap makes peak RSS track
bytes the bump allocator touches):

- **Profiling** (RSS of staged pipelines on a 600-function program): the
  optimiser is *not* the hog — stubbing `optimize` to identity *raised* RSS
  (it doubled the emitted IR). `build_func` is ~half the total; the rest is
  emit, scaling with output size.
- **Seed overlay** (landed) — the dominant *super-linear* term was
  `build_func` installing the module-wide signature seed (`gfn:` / method
  entries — ~one per compiler function) as each function's initial
  var-type list, so the first `set_var_type` copied the whole shared
  (rc>1) array: O(functions²). The seed now lives read-only in its own
  `BState` field; `get_var_type` consults the (initially empty) local
  overlay first, then the seed. Byte-identical output; peak RSS on a
  600-function string-typed module dropped 64.3 → 52.1 MB (~19%), and the
  saving *grows* with module size (4% at 100 fns → 19% at 600), i.e. it
  removes the quadratic term — the win compounds toward the real
  ~4000-function compiler.
- **Pass early-outs** (landed) — `copy_propagate` and `mark_*_calls` now
  return the function untouched (no rebuild allocation) when they have no
  work, which is the common case on every optimise round after the first.

Still open: the *linear* per-function retention (build + emit, freed by
nothing) — the streaming/arena or GC approach, deferred.

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
