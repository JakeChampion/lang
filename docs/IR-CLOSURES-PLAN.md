# Closures on the self-host IR path — implementation plan

## Why this doc exists

Goal 1 of the project roadmap is "a full IR implementation for the
*entire* language in the self-hosted compiler" — widen the IR subset
(`irlower.fern` → `asm_ir.fern` / `wasm_ir.fern` / `asm_arm64_ir.fern`)
until the legacy AST→asm fallback (`asm.fern` / `asm_arm64.fern` /
`wasm.fern`) is never taken. As of the 64-bit-container arc
(scalars, arrays, struct fields, methods, Option/Result, enum variants,
tuple elements, array-returning methods, tuple-returning functions —
PRs through #2739) the remaining **major frontier is closures /
lambdas**. Any program containing an `ExprLambda` — or even a bare
top-level function used as a value — currently falls back to the AST
backend because `lower_expr` has no case for it.

This document captures the design so the epic can be implemented as a
sequence of **atomically-landable** slices without re-deriving the
machinery each time.

## Which IR? (`irlower` vs `ssa.fern`)

The self-host has **two** IR layers, mirroring the two the native
compiler has:

- **`ir.fern` / `irlower.fern`** — the stack IR, mirroring native's
  `internal/ir`. This is "the layer where Perceus reference counting
  lives" (ir.fern header) and the one CLAUDE.md's goal 1 (widen the IR
  subset until the AST fallback is gone) and goal 2 (port Perceus to the
  self-host) both target. **This plan is for this path.**
- **`ssa.fern`** — mirrors native's downstream optimiser
  `internal/ssa`; a different, higher layer. Per
  `docs/SELFHOST-SSA-ALWAYS.md` it is the *default* lowering path for
  x86-64/arm64 in the unified driver (`fern.fern try_ssa`), and it has
  **already implemented closures** (Phase 2e), floats, generics — its
  `-ssa-scan` reports 100% per-function coverage, blocked only by the
  no-GC memory wall (which the Perceus track unblocks).

These are parallel efforts at the same end (retire the AST emitters).
Closures-on-`irlower` is therefore **not** made redundant by SSA already
having them — they are different layers — and the SSA implementation is a
ready-made **blueprint** for this port:

- `ssa.collect_lambdas` (lambda lifting: hoist `ExprLambda` to top-level
  `FuncDecl`s with a synthetic `__env` param) is directly reusable as
  the model for Slice 2's `closureconv.fern`.
- `ssa_wasm.fern`'s closure backend (Phase 2e) already solved wasm's
  strictly-typed `call_indirect`: an `SFunc.takes_env` flag, a function
  table whose slot holds each function's *closure-callable* form (the
  function itself if it takes `__env`, else an **env-dropping wrapper**),
  and a `$clos<arity>` type per arity. `wasm_ir` should mirror this
  exactly — note this means even the env-box ABI can be uniform if
  plain functions are wrapped rather than given a separate no-env
  `$fn<N>` type. (Slice 1 below keeps the simpler no-env path; revisit
  if uniformity with Slice 3 proves cheaper.)

## Hard constraints (why slices can't be tiny)

1. **One shared frontend, all-or-nothing eligibility.** All three
   backends route through the *same* `irlower.lower_func`; a function is
   IR-eligible iff `lower_func` returns `ok`, and the IR path only fires
   when the **whole module** is eligible (`asm_ir.all_eligible`, which
   `wasm_ir.wasm_eligible` simply delegates to). So the moment
   `lower_func` starts lowering lambdas/function-values, **every**
   backend must be able to emit the resulting IR ops — there is no
   wasm-only shortcut, and no per-target flag (the IR is deliberately
   target-agnostic).

2. **The `deadcode` CI job forbids unused code.** Helpers can't be
   landed ahead of their use. Each slice must be a complete, exercised
   feature.

Together these mean even the first slice (plain function values) must
land across `irlower` + `wasm_ir` + `asm_ir` + `asm_arm64_ir` at once,
with tests.

## How the AST backend does closures (the reference implementation)

References are to `examples/self_host/wasm.fern` unless noted; the
x86-64 mirror is `asm.fern`, arm64 is `asm_arm64.fern`, shared frontend
`asmcore.fern`.

### Type spelling

Function types are coarsened by the parser to the bare string `"fn"`
(`parser.fern:2310-2338`; `asmcore.fern:996` `TyFnAny → "fn"`,
`checker.fern:1751` `TypeFunc → "fn"`). So a param `f: () => i32` has
`type_name == "fn"`. A lambda-bound local used as a value may carry the
finer spelling `"fn:…"` (see `ssa.fern:1306`); the IR path can treat any
`"fn"`-prefixed type as a function value.

### Capture analysis (`lambda_captures`, wasm.fern:2259-2278)

Walk the lambda body collecting referenced idents
(`collect_idents_stmts`), subtract names bound inside the lambda (params
+ `collect_bound_stmts`), and keep those that are locals/params of the
enclosing function (`cx.all_locals`). Result: captured names in
first-seen order.

### Closure value layout (wasm.fern:5744-5782)

A closure is a 1-slot-plus-captures RC-boxed heap block, allocated via
`$__fern_str_box(4 + N*4)` (8-byte rc/bsz header at `[box-8]`):

```
[ table_idx : i32 ] @ box+0
[ cap0      : i32 ] @ box+4
[ cap1      : i32 ] @ box+8
...
```

RC-tracked captures (strings / arrays / structs) get a construction-time
`rc_inc`, balanced by a per-capture release when the closure dies
(`capture_kind`, wasm.fern:2280+).

### Hoisting (`emit_lambda`, wasm.fern:9362-9410)

Each lambda becomes a top-level function `$__lambda<idx>` whose **first
param is a synthetic `__env: i32`** (the closure box pointer), followed
by the lambda's declared params. The body reads a capture as
`load __env[4 + i*4]`. `idx` comes from a module-wide counter
(`cx.lam_ctr`), and the emitted text is accumulated in `cx.lamdefs`.

### Calls (wasm.fern:5474-5486, 5401-5413)

Calling a closure-valued local `f`:

```wat
(call_indirect (type $clos<N>) (local.get $f)   ;; env = box ptr (first arg)
   <arg0> ... <argN-1>
   (i32.load (local.get $f)))                    ;; table index = box[0]
```

`$clos<N>` is the signature `(param i32 {env}) (param i32 × N) (result
i32)`. The module epilogue (wasm.fern:10913-10930) emits, when
`lam_ctr > 0`: the `$clos<N>` type decls, the `$__lambda<i>` bodies, a
`(table <count> funcref)`, and `(elem (i32.const 0) $__lambda0 …)`.

The x86-64 / arm64 mirrors load `box[0]` into a scratch register and do
an indirect `call`/`blr`; there is no wasm-style table — the table index
is instead the function's own address taken at box-construction time.
(See asm.fern:2187-2257.)

### Existing IR ops (ir.fern)

Three ops are already defined but **emitted by no backend yet**:

- `op_const_func(table_idx)` — kind `"const_func"`, `i32_imm` = index.
- `op_call_indirect(argc)` — kind `"call_indirect"`.
- `op_call_closure(argc)` — kind `"call_closure_direct"`.

There are **no** `make_env` / `make_closure` ops yet.

### Native reference

`internal/closureconv/closureconv.go` is the native Go closure-conversion
pass — free-variable capture, `ptrW`-aware env-offset layout
(`captureSlotSize`, closureconv.go:445), lambda→`FuncDecl` hoisting with
a synthetic `__env` param, `CaptureRef{Offset,Type}` body rewriting,
`MakeClosure{FuncName,FuncIndex,Captures}` at the lambda site, and
Tarjan-SCC handling of mutually-recursive siblings (closureconv.go:123).
The IR port mirrors these concepts.

## Slice plan

### Slice 1 — plain function values (no captures, no lambdas)

Smallest atomic end-to-end unit. Covers programs that pass a *top-level
function by name* and call it through a function-typed param/local — e.g.
the `zero-arg-fn-value` and `predicate` cases in
`internal/e2e/self_host_closures_test.go`. **No env, no capture
analysis, no hoisting.**

ABI decision: a plain function value is just a **bare table
index / function pointer** (an `i32`), and `call_indirect(argc)` passes
**only the args, no env**. This is simpler than the AST env-box ABI and
sufficient for capture-free values; the env-box ABI is introduced in
Slice 3 for lambdas. The two coexist via the two distinct ops
(`call_indirect` = no-env, `call_closure_direct` = env-box).

Work:

- **ir.fern**: extend `op_const_func` to carry the function *name*
  (`str` field) so register backends can resolve it to a symbol; wasm
  resolves name→index via the address-taken table. Keep `i32_imm` for
  the resolved index where useful.
- **irlower.fern**:
  - Detect a top-level-function-named `ExprIdent` in value position →
    emit `op_const_func(name)`.
  - Lower a call whose callee is a `"fn"`-typed local/param →
    `op_call_indirect(argc)` (args then the function-pointer value).
  - Treat a `"fn"`-typed param/local as an `i32` slot (don't bail);
    allow `"fn"` params and `"fn"`-returning calls in the eligibility
    gate.
  - Add a module-level `addr_taken_fns_of(mod)` registry (ordered,
    deduped) of functions used as values, threaded into `LowerState`,
    so `const_func` indices and the wasm table agree.
- **wasm_ir.fern**: emit `(type $fn<N>)` decls for each used arg-count,
  a `(table <M> funcref)` + `(elem (i32.const 0) …)` over the
  address-taken set, `const_func` → `(i32.const <index>)`,
  `call_indirect` → `(call_indirect (type $fn<N>) <args> <fnidx>)`.
- **asm_ir.fern** / **asm_arm64_ir.fern**: `const_func` → load the
  function label's address (`lea` / `adrp+add`); `call_indirect` →
  marshal args per the SysV / AAPCS ABI and indirect-`call`/`blr`.
- **Tests**: a new `internal/e2e/self_host_fnval_ir_test.go` with
  hardcoded-oracle wasm-IR cases (function value bound + passed +
  called, predicate-over-array), plus an eligibility probe like
  `TestSelfHostIRTupleReturnEligible`. Gate: wasm e2e + x86-64 fixpoint
  locally, arm64 on CI.

### Slice 2 — capture analysis + hoisting pass (`closureconv.fern`)

Port `lambda_captures` + lambda→FuncDecl hoisting into a standalone
Fern module that, given a `parser.Module`, returns the module with
lambdas hoisted to top-level `__lambda<idx>` functions (synthetic
`__env` first param) and lambda sites replaced by a `make_closure`
marker carrying the captured exprs. Runs **before** `lower_func`. Unit
-tested via a probe that checks captured-name sets and hoisted-function
counts by exit code. (This slice changes no eligibility on its own — it
is wired in by Slice 3 — so to satisfy `deadcode` it lands *together
with* Slice 3, or its first consumer.)

### Slice 3 — capturing lambdas (env-box ABI)

- New ops `op_make_closure(name, ncap)` / capture-store, and
  `op_call_closure(argc)` lowering (env-first).
- `irlower` lowers the `make_closure` marker → box alloc
  (`__fern_str_box`), store table_idx@0 + captures@4+i*4, with the
  `ptrW`-aware 8-byte slots already used elsewhere (`WidthPtr`).
- Capture reads inside the hoisted body → load from `__env + off`.
- Backends emit the env-box construction + the env-first
  `call_closure_direct`, mirroring wasm.fern:5744-5782 / 5474-5486 and
  the register mirrors.
- Perceus: closure boxes are RC values; captured strings/arrays/structs
  inc at construction and dec at closure death (`capture_kind`). This
  must integrate with the IR path's existing RC insertion.
- **Tests**: the remaining `self_host_closures_test.go` cases
  (`capture-local`, `multi-capture`, `capture-string`,
  `return-closure`) re-expressed as wasm-IR oracle cases.

### Slice 4 — closures returning closures / nested + mutual recursion

`return-closure` already lands in Slice 3 for the single level; this
slice covers nested lambdas (lambda inside lambda — capture chaining)
and the Tarjan-SCC mutual-recursion handling from the native pass
(closureconv.go:123-187), if/when the self-host needs it.

## Validation per slice

- wasm e2e via the `wasm_ir_run.fern` driver (`-ir`), hardcoded-oracle
  exit codes (never AST-vs-IR differential alone — both can agree on a
  wrong answer).
- x86-64 self-host fixpoint (`TestSelfHostFixpoint`,
  `TestSelfHostLoadFixpointX86_64`) — the compiler compiling itself.
- Eligibility probe locking in the newly-eligible shapes.
- arm64 e2e + fixpoint left to CI (the shared frontend means an
  x86-green change is almost always arm64-green; CI is the backstop).
