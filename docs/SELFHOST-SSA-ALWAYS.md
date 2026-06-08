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

**The last coverage blocker is `main` — a feature *cluster*, not a one-liner.**
`main` is the CLI driver, and its remaining SSA gaps are all I/O / sum-type:
- **File-I/O builtins** `read_file` / `write_file` — these are *builtins*
  (syscall sequences the AST backend emits inline; no `function read_file`
  exists), so the SSA backends would emit `call read_file` → unknown. They
  need dedicated SSA ops + an open/read(/write)/close syscall runtime on
  x86-64 / arm64 (wasm via WASI `path_open` etc., or fall back).
- **Option / Result** (`Some`/`None`/`Ok`/`Err`) — `build_match` only
  handles user struct-unions (tag = struct index); these builtin sum types
  use a fixed 2-word box `{tag@0, payload@8}` (Some/Ok = 0, None/Err = 1).
  Needs: construction in `build_func` (mirror `asm.fern`), and a `build_match`
  path that dispatches on the fixed tag and binds the payload from word 1
  (the user-variant path binds the whole box, so this is a distinct case —
  cleanest via a synthesized `__opt_payload` load builtin so the AST-level
  `synth_match_chain` still works). Payload typing is best-effort (in `main`
  the bindings are only assigned or unused, so an empty type suffices).

This (file I/O + sum types) is the most Perceus-adjacent remaining work and
warrants deliberate, coordinated implementation rather than a tail-end rush.

Still open: the *linear* per-function retention (build + emit, freed by
nothing) — Perceus RC (the native backend already has it; the self-host
runtime stubs it to no-ops — `asm.fern`: "leak-everything bump heap") or a
streaming/arena scheme; and `main`'s I/O + sum-type cluster above.

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
