# Self-hosted compiler: SSA always-on, every backend on the IR

> **⚠️ SHELVED (2026-07-03, #4391).** This plan is superseded. The self-host
> backend has **one** production lowering — the stack **IR** path
> (`irlower.fern` → `asm_ir` / `asm_arm64_ir` / `wasm_ir`), not SSA
> `build_func`. See **`docs/SELFHOST-SSA-DECISION.md`** for the decision and
> rationale. Concretely: `fern.fern`'s default is flipped — SSA is now opt-in
> via `-ssa` (experimental), so **Phases 1–4 below are shelved**. The SSA
> framework, its optimiser, its register allocator, and `ssa_lift.fern`
> (stack-IR → SSA, the *downstream* optimiser entry) are **kept**; only the
> redundant `build_func` frontend is slated for retirement. The historical
> record below is preserved for context — it is no longer the roadmap.

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

## Note: an SSA-from-IR lift (the native-shaped entry)

`ssa.build_func` builds SSA straight from the **AST**, in parallel with the
stack IR (`ir.fern` / `irlower.fern`) that is now the default lowering path
(goal 1). Native takes the other route: it lowers AST → `ir.Op[]` and then
**lifts** that stack stream into SSA (`internal/ssa/lift.go`'s `LiftFromIR`)
ahead of its optimiser and the `-backend ssa` (wasm) backend. Lifting from the IR makes
SSA a clean *downstream* layer over the IR the compiler already produces —
so it inherits the IR's (now ~100%) coverage instead of re-deriving it in a
second all-or-nothing frontend.

**Slice 0 ✅ (landed — `ssa_lift.fern`):** `lift_from_ir(name, nparams,
nslots, ops) : LResult` lifts the straight-line integer spine (`const_i32`,
`load_local` / `store_local` / `tee_local`, the integer binary ops, the `not`
unary, `call_direct`, `drop`, `return`) plus structured **void `if` / `else`
/ `end`** with phi reconstruction at merges — the canonical SSA win: locals
that differ across the two arms become block phis, exactly as native's lift
does, and `if`-without-`else` phis the then-arm against the fall-through.
Anything outside the subset (`block` / `loop` / `br` / `brif`, value-producing
ifs, memory / struct / map / float ops) returns `ok=false` so a caller falls
back, mirroring how native's lift errors. The lift is a single linear pass
(the IR op kinds, e.g. `add` / `lt_s`, are translated to the SSA `binary`
symbols `+` / `<` that `eval_binary` and the SSA backends switch on). Validated
without a backend by running the lifted `SFunc` through `ssa.eval_func` on both
arms of each branch case (the ssa.fern pattern). Gated by
`self_host_ssa_lift_test.go` (driver: `ssa_lift_run.fern`).

**Slice 1 ✅ (landed — loops):** structured `block` / `loop` / `br` / `brif`
(BlockTypeVoid) — i.e. real loops. The lift now tracks `if`, `block` and `loop`
on ONE unified scope stack so `br` / `brif` relative depths resolve uniformly,
and reconstructs **loop-header phis**: at a `loop` it emits a placeholder phi
per local into the header, threads the entry edge + each `br`-back-edge as a
predecessor (recorded as a global edge tagged by scope uid), and fills the phi
operands when the loop's `end` is reached — exactly native's approach. A
`block`'s `end` opens its exit, merging every `br`/`brif`-out edge (plus a live
fall-through) with a phi where the incoming values differ. This is the canonical
`while` shape `irlower` emits (`block; loop; <cond> not; br_if 1; <body>; br 0;
end; end`). Validated by running lifted loops through `ssa.eval_func` across
iterations — `while`-sum (loop-carried `i`/`acc` phis) and an `if` nested inside
a loop (exercising the unified scope-depth resolution).

**Slice 2 ✅ (landed — `break` / `continue` + dead-arm cleanup):** `break`
(a `br` to an enclosing `block`'s exit) and `continue` (a `br` to an enclosing
`loop`'s header) already routed through the generic `br`/`brif` edge machinery,
but an arm that branched out left a **phantom dead block** with an empty
terminator that the `if`/`else` merge then wired in as a bogus predecessor —
invalid SSA that `eval_func` only tolerated because the block was unreachable.
Fixed by folding the `cur_dead` flag into the arm-reaches test (`cur_term == ""
&& !cur_dead`) and skipping the append of an already-branched-out arm, so a
`break`/`continue` arm contributes no edge and emits no orphan block (and a
both-arms-diverge merge is correctly marked unreachable). Validated by running
lifted `break` and `continue` loops through `ssa.eval_func` and by inspecting
the emitted SSA (no dead predecessors). Still out of subset (→ `ok=false`):
value-producing ifs (`BlockTypeI32`+; `irlower` doesn't emit them — it carries
values through locals) and memory / struct / map / float / i64 ops.

**Slice 3 ✅ (landed — the lift feeds real codegen):** the milestone — the
lift's SSA is no longer only interpreted (`eval_func`); it now flows through the
existing optimiser and backend to **native machine code**. A new driver
(`ssa_lift_emit_run.fern`) builds a hand-coded `ir.Op[]` program, lifts it
(`lift_from_ir`), runs `ssa.optimize` over the result (which proves the lifted
SSA is accepted by the existing optimiser unchanged), and emits x86-64 via
`ssa_x86.emit_program` — exactly the pipeline `ssa_emit_run` runs, with
`lift_from_ir` swapped in for `build_func`. The Go gate
(`self_host_ssa_lift_emit_test.go`) assembles the output (`gcc -static -nostdlib
-no-pie`) and runs it, asserting the process exit code equals the program's
value, across four programs (straight-line arithmetic, a `while`-loop sum,
a void if-merge, and a `break`) **each in both the default slot-addressed emit
and `-regalloc`** (the linear-scan allocator over lifted SSA). This is the
end-to-end proof of the `stack-IR → SSA → backend` thesis on real hardware: the
IR that is already the default lowering path lifts to SSA that the existing
optimiser and x86-64 codegen consume all the way to a running binary.

**Slice 4 ✅ (landed — arm64 backend parity):** the lifted SSA now feeds the
*second* production backend too. `ssa_lift_emit_run` grew a `-target arm64`
that routes the same lifted `SFunc` through `ssa_arm64.emit_program` (the lift's
SSA is target-agnostic — one `SFunc` feeds either backend, exactly as
`build_func`'s output does in `ssa_emit_run`). `self_host_ssa_lift_emit_test.go`
now runs each of the four programs on **both** targets — x86-64 natively and
arm64 under qemu — each in the default slot-addressed emit and `-regalloc`, so
all sixteen combinations assemble and execute to the right exit code. This
matters because arm64 is the project's *default* target; the lift now has
backend parity for the integer control-flow subset.

**Slice 5 ✅ (landed — multi-function programs + calls):** the lifted-emit path
went from a single `main` to a **list of functions** lifted and emitted
together, so cross-function `call_direct` resolves. A program is now an ordered
`FnSpec[]` (entry `main` first); the driver lifts each, runs `ssa.optimize`, and
hands the whole set to `emit_program`. Two new programs join the matrix: `callsum`
(`main()` calls `add(20,22)` → 42 — a plain cross-function call) and `factrec`
(`fact(5)` → 120 — **self-recursion** through the lifted SSA, exercising a call
inside an `if`/return with loop-free recursion). Both run on x86-64 + arm64,
default + `-regalloc`. `call_direct` already lowered in slice 0; this proves it
links and executes once the callee's lifted `SFunc` is emitted alongside.

**Slice 6 ✅ (landed — differential gate vs. the interpreter):** the lift
codegen path is now cross-checked against an INDEPENDENT oracle — the native
tree-walking interpreter — rather than hand-derived constants.
`self_host_ssa_lift_diff_test.go` pairs each program's hand-coded `ir.Op[]`
(lifted → emitted → assembled → run on x86-64) with an *equivalent Fern source*
run through `fern -interp`, and asserts the two exit codes agree. The `Op[]` and
the source are equivalent by construction (same program, two front-ends); the
interpreter is the source of truth for the value. So a miscompile anywhere in
`lift_from_ir` / `ssa.optimize` / `ssa_x86` now surfaces as a divergence from
reference semantics, across all six programs (straight-line, loop, if-merge,
break, cross-function call, recursion).

**Slice 7 ✅ (landed — real `irlower` output through the lift):** the lift now
consumes the ACTUAL production IR, not hand-built `Op[]`. A new driver
(`ssa_lift_irlower_run.fern`) reads a Fern source, lowers each function the way
the real compiler does — `irlower.lower_func_for` (AST → `ir.Op[]`, the asm_ir
backend's input) — then lifts that real IR to SSA and emits via `ssa_x86` /
`ssa_arm64`. Driving real `irlower` exposed two ops the lift hadn't needed for
hand-built programs: **`const_i32_text`** (irlower carries integer literals as
source text — the lift parses the decimal; hex/non-decimal bails) and
**`int_cast "i32"`** (the `movslq` sign-extend that's identity for an i32 value,
lifted as a pass-through; subword/unsigned casts bail). With those, the same six
programs (straight-line, loop, if-merge, break, cross-function call, recursion)
go source → `irlower` → lift → SSA → x86-64 / arm64 → run, and
`self_host_ssa_lift_irlower_test.go` checks each emitted binary's exit code
against the **interpreter** for the same source. This is the step that turns the
lift from a proof-of-concept over synthetic ops into a path over the real
production IR.

**Slice 8 ✅ (landed — string literals + length):** the first non-integer
widening of the real-`irlower` subset. Probing richer programs mapped the
boundary precisely: **arrays** lower with Perceus RC ops (`call_direct
__fern_rc_dec`) that don't link in the SSA backends — out of subset for now —
but **a string's literal + `.len()`** lowers RC-free (`const_str` /
`store_local` / `load_local` / `str_len`). So the lift now handles `const_str`
(→ the SSA `const_str` block) and `str_len` (→ `load_elem(s, 0)`, the word-0
length, exactly how `build_func` lowers `.len()`). Real programs that build a
string literal and compute over its length — including `if` on a length
comparison — now go source → `irlower` → lift → SSA → x86-64 / arm64 → run,
checked against the interpreter (`strlen` / `strlen2` / `strpick` cases).

**Slice 9 ✅ (landed — hardening the real-`irlower` subset):** with the next
widening gated by the RC boundary (below), this slice deepens confidence in
what already lifts. Six harder shapes were probed through `irlower` and added
to the differential gate (all green on x86-64 + arm64 vs. the interpreter):
**nested loops** (a `while` inside a `while` — nested loop-header phis),
**mutual recursion** (`isodd`/`iseven` calling each other), the **bitwise**
(`&`/`|`/`^`) and **shift** (`<<`/`>>`) operators, a **nested `if`** with
multiple return paths, and an **early `return` out of a loop**. No lift changes
were needed — they all already lower and run correctly — so this is pure
coverage that pins the integer/string subset against real production IR before
the RC boundary.

Next: the array / heap subset is gated by the Perceus RC `call_direct`s the
lowered IR inserts — lifting those needs the SSA backends to provide (or the
lift to drop, where sound) the `__fern_rc_*` / `__fern_arr_*` runtime. That
boundary is the natural meeting point with goal 2 (the Perceus port) — until
then, the lift covers integer control flow + string-length over real `irlower`
output, the production-shaped path proven end-to-end on both backends.

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
- **SSA heap 16 MiB → 1 GiB** (landed) — the SSA backends emitted a 16 MiB
  `__fern_ssa_heap`, sized for small programs; a *self-hosted* SSA compiler
  is itself leak-everything, so even a modest real compile overruns it
  (a ~21 MiB allocation segfaults). Raised the native (x86-64 / arm64)
  arena to 1 GiB, matching the AST backend's proven `__fern_heap`. It is
  `.bss` (demand-zero): no disk cost, no resident memory until touched. wasm
  is left at 16 MiB — it is not a self-host target and that suits ordinary
  wasm programs. Gated by a `heap-beyond-16mib` emit case.

- **Bootstrap heap 512 MiB → 1 GiB** (landed) — `cmd/fern`'s emitted
  runtime (`internal/codegen/{x86_64,arm64}`) `mmap`s a single fixed
  `heapBytes` arena and cleanly traps (exit 137) on overflow. It was
  512 MiB — enough for `lexer.fern` but **not** for a `cmd/fern`-built
  self-host compiler to bootstrap-compile the *whole* self-host source in
  one process. Raised to 1 GiB (lazy-mapped, free until touched; kept
  ≤ INT32_MAX so the `lea`/`mov esi` size operands stay valid). With it,
  `cli` compiles the entire unified `fern.fern` (+ all modules + stdlib)
  end-to-end at **~0.75 GiB**. (Unrelated to the self-host `__fern_heap`
  the `alloc-trap` test exercises — that stays 1 GiB.)

### What actually blocks SSA self-hosting (measured)

An earlier note here mis-read a `cmd/fern` exit-137 (the 512 MiB trap
above) as a ">15 GB blowup". Corrected: with the 1 GiB bootstrap heap,
`cli` compiles the whole `fern.fern` fine — but the **default path falls
back to the AST emitter** (the output is byte-identical to `-no-ssa`).
`try_ssa` is all-or-nothing, and on the full compiler it bails on a small,
*enumerated* set of gaps (not generics, not memory):

- **Unknown callees** (emitted by `build_func` but absent from the SSA
  `known` set, and not provided by the SSA backends' runtime):
  `strbuf_reset` / `strbuf_append` / `strbuf_take` (the amortised
  string-builder the AST backends use for output), ~~`exit`~~ (✅ landed —
  a dedicated `exit` SSA op lowering to the exit syscall on x86-64/arm64,
  kept through DCE; wasm falls back), ~~`f64_bits` / `f64_from_bits`~~
  (✅ landed — a `unary` bit-reinterpret: an 8-byte slot pass-through with
  `compute_isfloat` marking `f64_from_bits` float / `f64_bits` not, and
  `compute_widths` making both 64-bit; x86-64/arm64 emit a `movq`/`ldr+str`
  slot copy, wasm rejects it via `inst_supported` → AST fallback since the
  i64 pattern doesn't fit wasm32's i32 value model), and bare
  receiver-method calls `cur_id` / `w` (a `type_of_expr` receiver-resolution
  gap).
- **`build_func` failures** in exactly two functions: `main` (the CLI
  driver) and `wasm__build_ctx`.

**Re-measured (current main).** Re-running the instrumented `try_ssa` on the
whole `fern.fern` shows the list has collapsed to a **single** remaining
blocker — the `cur_id` / `w` receiver-method calls and **both** `build_func`
failures (`main`, `wasm__build_ctx`) are gone, resolved by intervening
frontend fixes (param-spread, extern arrays, …) on top of the `exit` and
`f64_bits` ops above. The sole remaining unknown callee is the
**`strbuf_*` string-builder** (`strbuf_reset` / `strbuf_append` /
`strbuf_take`): a global amortised-O(1) output accumulator used by `asmcore`
(the shared frontend) and the AST emitters.

**`strbuf` ✅ landed** — the last *coverage* blocker. A dedicated op each
(`strbuf_reset` / `strbuf_append` / `strbuf_take`) with a re-derived SSA
runtime: a global byte buffer in `.bss` (256 MiB, demand-zero) + a length;
`append` copies a source string's bytes out of the `[len, byte-per-word]`
layout, `take` materialises a fresh `[len, bytes…]` string and clears the
buffer. x86-64 + arm64 emit it (validated under qemu); wasm rejects it via
`inst_supported` → AST fallback (it can't be a pure-Fern helper — the SSA
subset has no global mutable state). Gated by `strbuf-build` / `strbuf-reuse`
cases on both native backends.

**Correction — coverage was NOT complete after `strbuf`.** That earlier
claim was wrong: `try_ssa` reports only the *first* bail per run, so each
fix unmasks the next. Driving an actual full-`fern.fern` SSA compile (with
extra bootstrap headroom to get past the AST-fallback memory) and a
*non-bailing* instrumented scan (record every `build_func` failure and
unknown callee across all functions in one pass) gives the true remaining
set:

- **A parser bug** (✅ fixed here): `parse_struct_decl` parsed a phantom
  empty-named field for a struct decl with a **trailing comma** after the
  last field (it consumed the `,` then unconditionally parsed another field
  instead of checking for `}`). `Ctx` (in `wasm.fern`) is the one struct so
  written, so `build_ctx`'s `Ctx { … }` literal (40 fields) failed to match
  the decl's phantom 41st → `build_func` bailed. The Go parser and the
  struct-*literal* parser already handled trailing commas; this aligns the
  struct-*decl* parser. Gated by a parser self-test.
- **Still open** (the genuine remainder): `main` fails `build_func` (one
  unhandled construct in the CLI driver), and the receiver-method calls
  `w` (38, `WState.w` in `wasm.fern`) and `cur_id` (3, `BState.cur_id`) are
  unknown callees — a `type_of_expr` receiver-resolution gap (the dispatch
  falls back to a bare method name when the receiver's type isn't inferred).
- **And then** the cumulative-allocation wall: even once those are closed,
  a full SSA self-compile exceeds the 1 GiB `cmd/fern` bootstrap heap
  (~0.85 GiB resident before falling back today; the SSA path needs more) —
  the no-GC retention below.

(Small programs are unaffected and all SSA suites stay green; only the
whole-compiler self-compile reaches any of this, and no test exercises it.)

**Update — `w` / `cur_id` ✅ fixed (#2388).** They were *not* a
`type_of_expr` gap: `build_func`'s `ExprIdent` arm tested the function-value
type before the local binding, so a local var named like a function/method
(loop var `w`; block-id var `cur_id`) became a `funcaddr`. Fixed by checking
`get_var` first (a local shadows a same-named function). With that, the
whole `fern.fern` has **zero unknown callees** and exactly **one**
`build_func` blocker left: **`main`**.

**The last coverage blocker was `main` — a feature *cluster*, not a one-liner
(now ✅ fully closed).** `main` is the CLI driver, and its remaining SSA gaps
were all I/O / sum-type:
- **Option / Result** (`Some`/`None`/`Ok`/`Err`) — ✅ **landed.**
  `build_func` now constructs the fixed 2-word box `{tag@0, payload@1}`
  (Some/Ok = 0, None/Err = 1, mirroring `asm.fern`), `None` is an `ExprIdent`
  box, and `build_match` / `synth_match_chain` dispatch on the fixed tag and
  bind the payload from word 1 via a synthesized `__opt_payload(box)` load
  (so the AST-level chain still works; distinct from the user-variant path
  which binds the whole box). All heap ops, so wasm gets it for free.
  Payload typing is best-effort. Gated by an `option-result` case on the
  x86-64 / arm64 / wasm SSA emit matrices.
- **File-I/O builtins** `read_file` / `write_file` — ✅ **landed.**
  Dedicated SSA ops (`build_func` recognises the builtin calls, like `exit` /
  `strbuf_*`) lowering to a syscall runtime on x86-64 + arm64:
  `__fern_ssa_read_file` (openat O_RDONLY → lseek-size → read-to-EOF → a fresh
  `[tag=0, payload=string]` Ok box, or `[tag=1, 0]` Err on error) and
  `__fern_ssa_write_file` (openat O_WRONLY|O_CREAT|O_TRUNC → write → a
  `[tag=1, 0]` None box, or `[tag=0, 0]` Some on error). The key wrinkle vs the
  AST backend: SSA strings are `[len, byte-per-8-byte-word]` (not the AST
  `{ptr,len}` over packed bytes), so both helpers pack/unpack between that
  layout and the raw byte buffers the syscalls work on. Allocation is a shared
  `__fern_ssa_alloc` bump off `__fern_ssa_hp`. wasm rejects them via
  `ssa_wasm.inst_supported` → AST fallback (its WASI `path_open` path). Gated
  by `file-io-roundtrip` / `-read-external` / `-read-missing` cases (default +
  `-regalloc`) on the x86-64 + arm64 SSA emit matrices.

  Surfaced and fixed on the way: an all-arms-`return` `match` used as the last
  statement (the idiomatic `match (read_file(p)) { Ok(s) => return …, Err(e)
  => return … }`) failed `build_func`. `synth_match_chain` always emitted an
  empty trailing `else` after the final variant arm, whose dead-but-reachable
  un-terminated fall-through tripped `build_func`'s "missing return" bail. Fix:
  the textually-last variant arm of an (exhaustive) match is now an
  unconditional catch-all — like a wildcard — so no empty `else` is generated.
  Behaviour-preserving for exhaustive matches (the only kind the checker
  admits), and a strict IR simplification for the rest.

With this, **`main`'s `build_func` succeeds** and file I/O is off the blocker
list. But — true to the "each fix unmasks the next" pattern above — file I/O was
*not* the last coverage gap.

**Measured (the `-ssa-scan` diagnostic).** `fern.fern` grew a `-ssa-scan` flag:
the non-bailing twin of `try_ssa` that runs `build_func` over every function in
the merged program and records *all* failures + *all* unknown callees in one
pass (vs `try_ssa`'s first-bail, all-or-nothing). Run over the whole compiler
(1499 functions after lambda lifting) on current `main` + this PR, the true
remaining coverage set is **small and enumerated**:

- **`build_func` failure (1):** `wasm__emit_expr` — one unhandled construct in
  the AST wasm emitter's expression lowering.
- **Unknown callees (2):** `write` (raw stdout write — `print` *without* the
  trailing newline; the driver's output primitive) and `args` (the argv
  builtin returning `string[]`). Both are builtins with no `function` body, so
  the SSA backends emit `call write` / `call args` → unknown. They need
  dedicated ops + runtime, exactly like `read_file` / `write_file` / `exit` /
  `print` before them (`write` is trivial — `print` with a no-newline flag;
  `args` needs an argv→`string[]` materialiser over the `_start`-saved vector).

Those three holdouts have since been closed:

- **`write`** ✅ — a dedicated op → `__fern_ssa_write` (`print` minus the
  trailing newline).
- **`args`** ✅ — a dedicated op → `__fern_ssa_args`, materialising the SSA
  `[argc, strptr…]` array of SSA strings from the argv pointer the SSA
  `_start` now saves.
- **`wasm__emit_expr`** ✅ — *not* a missing feature but a `type_of_expr`
  field-normalisation gap: a Cell-typed **struct field** (`cx.lam_ctr:
  Cell[i32]`) accessed as `cx.lam_ctr.get()` reported its raw declared type
  `"Cell[i32]"`, which the cell-method path's `urt == "cell"` check missed →
  `build_func` bail. `type_of_expr`'s field arm now normalises a field's type
  the same way params are (`Cell[…]` → `"cell"`, `Map[…]` → `"Map"`/`"SMap"`),
  so Cell / Map struct fields dispatch their methods. Gated by a
  `cell-struct-field` case on the x86-64 + arm64 SSA emit matrices.

**`-ssa-scan` over the whole compiler (1505 functions) now reports
`0 failures, 0 unknown callees` — per-function SSA coverage is genuinely 100%.**
Every function the self-host compiler defines lowers through `build_func`.

The *only* thing now between here and a byte-stable SSA self-compile fixpoint
(Phase 4's success criterion) is the **memory wall**.

#### Measured (current `main`, x86-64, RSS of a full whole-compiler compile)

| path | result | peak RSS |
|------|--------|----------|
| `-no-ssa` (AST emitter) | ✅ completes, 28 MB asm | **726 MB** |
| default (SSA pipeline)  | ✗ **traps (exit 137)** | **~1 GiB** (overruns the `cmd/fern` bootstrap heap; needs **>2 GiB** — a 1.9 GiB heap still trapped) |

Note the *behaviour change* 100% coverage brought: previously the default path
bailed **early** (a coverage gap → `try_ssa` returns `ok=false` cheaply → AST
fallback). Now that every function is in-subset, `try_ssa` proceeds through the
**entire** build of all ~1500 functions before it could fall back — and OOMs
mid-build. (Users are unaffected: only a ~1500-function in-subset program — i.e.
the self-compile itself — reaches this; ordinary small programs compile fine.)

#### What the wall is NOT (measured, not assumed)

The intuitive culprit — `try_ssa` holding **all** the final `SFunc`s in
`sfuncs[]` until `emit_program` — was **tested and ruled out**. A streaming
rewrite (build → `emit_one` → drop each function, so RC could reclaim it; the
backends split into `emit_prologue`/`emit_one`/`emit_epilogue`) moved peak RSS
by **3 MB** (982 → 979) and still trapped. The reason: `try_ssa` is
all-or-nothing, so even a streaming driver must **build every function once** up
front to verify the whole program is in-subset — and *that first build pass
alone* overruns the heap.

So the wall is **transient allocation inside `build_func` + `optimize`**, not
retained output. Those passes churn `.append` / functional-update garbage per
function, and **nothing is freed**: the `cmd/fern`-built compiler is at RC
"free OFF" (the Perceus self-host port is mid-flight — see #2491 "string RC
counting milestone (free OFF)" and `docs/RC-PERCEUS-SELF-HOST-PORT.md`). With
freeing off, total transient allocation across ~1500 builds exceeds the arena
regardless of how the output is streamed. The AST path fits only because its
total churn happens to stay under 1 GiB.

#### The actual unblock (two viable levers)

1. **Turn RC freeing ON** (the Perceus track already in progress). Once dead
   per-function build garbage is reclaimed, the streaming-emit structure above
   becomes worthwhile and the self-compile should fit. This is the project's
   chosen direction.
2. **Cut `build_func` / `optimize` transient allocation** (the seed-overlay and
   pass early-out fixes already did this once, removing an O(functions²) term).
   A further reduction could get total churn under the arena even with free off.

Streaming the emit is *necessary but not sufficient* — keep it for after
freeing lands; on its own it does nothing.

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
