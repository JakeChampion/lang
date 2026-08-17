# Closure conversion for the self-host IR path — design + rollout

Status: **shipped** (design started 2026-06-16; the capturing-closure-arg
lowering landed — see §6 phases 1–2). Goal-1 increment: widen the self-host
IR subset to cover **capturing closures passed as function arguments** (the
`map` / `filter` / `fold`-with-a-capturing-closure pattern), the last common
first-class-function gap on the IR path. Capturing lambdas hoist to a `$clo`
env and box `[fnptr, caps…]` at the call site; gated x86-64 + wasm by
`TestSelfHostCaptureLambda{X86,Wasm}IR` (slice 2c) and the closure-env
capture-borrow fix (#4354). Phase 3 (fn-values in method / struct-field /
tuple-element positions) is the remaining extension.

## 1. The gap

| form | routes today |
|---|---|
| non-capturing lambda as arg (`apply(x, (n) => n*3)`) | **ir** |
| top-level fn as arg (`apply(x, inc)`) | **ir** |
| fn-value local (`var f = inc; f(5)`) | **ir** |
| fn-pointer array (`[inc, dbl][i](5)`) | **ir** |
| capturing lambda called **directly** (`var f = (n)=>n*k; f(3)`) | **ir** (param-lift) |
| escaping capturing lambda (`return (n)=>n*k`) | **ir** (`$clo` box) |
| **capturing lambda as arg** (`apply(x, (n)=>n*k)`) | **ast** ← the gap |

Verified with `asm_pathprobe_run` (production pipeline) — see the probe battery in
the 2026-06-16 session notes.

## 2. Why it is a port, not a one-line fix

Two call protocols coexist in `irlower.fern`'s `ExprCall` lowering:

- **Bare fn-pointer** — `push args; push fnptr; call_indirect(arity)`. Used by a
  fn-typed PARAM call (`irlower.fern` ~3947), a fn-value local, a fn-pointer array
  element, and a non-capturing lambda arg (lifted to a bare `__lam_N` by
  `lift_lambdas` step 2 / `lift_call_arg`).
- **Env-first box** — `push box (as __env); push args; push box[0] (target);
  call_indirect(arity+1)`, target signature `(__env, params…)`. Used by a closure
  LOCAL (`is_closure_local`, ~3933), a closure-array element, and the escaping
  `return <capturing lambda>` path (`hoist_escaping_closure` → `<fn>$clo`).

A capturing lambda arg needs the env-first box (to carry its captures), but the
callee invokes a fn-typed param via the **bare** protocol — and the callee is
compiled once, so the param needs ONE representation. The four bare-fn forms above
all route IR today and are guarded by `arrow_lambda_test.go` + the self-host
closure e2e tests, so the representation must be **unified** without regressing
them: every fn-value becomes a box, every callee fn-param call goes env-first.
That is closure conversion (the native compiler's `closureconv`), touching every
fn-value site — hence a port.

## 3. Feasibility: fixpoint-safe

The port was designed while the self-host compiler's own sources used **no**
first-class functions, so changing the fn-value representation in `irlower`
could not change how the compiler compiled itself and the Stage-2 byte-identical
fixpoint was unaffected. That is no longer true: `astwalk.fern`'s `fold_expr` /
`fold_stmt` take a fn-typed parameter and `collect_calls_stmt` supplies it as a
capturing nested function (#6993), so the fixpoint now DOES see this
representation. The constraint on further work here is therefore both the
USER-PROGRAM closure tests and the fixpoint.

## 4. Design — uniform boxed fn-values + env-first calls

Representation: a fn-value is always an `i32[]` box `[fnptr, cap0, cap1, …]`
(`make_closure`/`make_env`, already emitted for escaping closures). A
non-capturing value is a 1-element box `[fnptr]`. Every callable reachable as a
value takes `__env` as its first param (captures read from `__env[1+i]`).

Call protocol (uniform, all fn-value call sites): `push box (as __env); push args;
push box[0]; call_indirect(arity+1)`.

Producers:
- **lambda arg** (capturing or not): hoist to `<host>$clo_N(__env, params…)`
  reading captures from `__env`; build the box `[const_func($clo_N), caps…]` at
  the call site. Reuse `lambda_captures` + the escaping-closure box construction.
- **top-level fn as value** (`inc`, `[inc, dbl]`): wrap in a 1-element box
  `[const_func(inc$wrap)]` where `inc$wrap(__env, params…)` ignores `__env` and
  tail-calls `inc(params…)` — a generated `__env`-ignoring trampoline (one per
  top-level fn used as a value). Alternatively, drop `__env` for these by making
  `box[0]` carry an arity tag; the trampoline is simpler and matches `$clo`.

Consumers (all switch to env-first): fn-typed param call (~3947), fn-value local
(was bare), fn-pointer array element (~4353), fn-value tuple element (~4379),
"fn"-typed struct field call. The existing closure-local / closure-array / `$clo`
paths are already env-first and stay.

## 5. Change points (exact)

- `irlower.fern`
  - `ExprCall` → `ExprIdent` callee, slot case (~3947): bare → env-first box.
  - `ExprCall` → `ExprIndex` callee, plain fn-ptr-array case (~4353): env-first.
  - `ExprCall` → `ExprFieldAccess` callee, "fn" tuple element (~4379) + "fn"
    struct field: env-first.
  - call-arg lowering, `callee_param_is_fn` (~4291) and the method mirrors
    (~4749/4831/4860/4912): emit a box (1-elem for a top-level fn / non-cap
    lambda; N-elem for a capturing lambda) instead of a bare `op_const_func`.
  - `lift_lambdas` step 2 (`lift_call_arg` ~13590): hoist a CAPTURING lambda arg
    to a `$clo`-style `__env`-taking target (today only no-capture is hoisted);
    generate `__env`-ignoring trampolines for top-level fns used as values.
  - eligibility (`ir_eligible` via `lower_func_for`) then accepts the case.
- `wasm_ir.fern` / `asm_arm64_ir.fern`: the env-first box ops (`call_indirect`,
  `arr_get` for `box[0]`) already emit on every backend; verify the funcref-table
  path handles the extra `__env` arg uniformly (it already does for closure
  locals).

## 6. Phased rollout (each phase: route `"ir"` + oracle-checked, x86-64 + wasm,
fixpoint green)

1. **Trampolines + uniform producer for top-level fns / non-cap lambdas.** Box
   them `[fnptr]` with `__env`-ignoring `$wrap`; switch the four bare consumers to
   env-first. Net behaviour unchanged — but now everything is env-first. Gate:
   `arrow_lambda_test.go`, `hof-noncap-*`, `fnval-local`, `fnptr-array`,
   `topfn-as-arg` all still green.
2. **Capturing lambda arg.** Hoist to `$clo`, box `[fnptr, caps…]` at the call
   site. Gate: new `TestSelfHostCapturingClosureArgIR{X86_64,Wasm}` —
   `apply(x, (n)=>n*k)`, multi-capture, capture-used-twice, two HOF params, plus
   `hof-noncap` / `fnptr-array` regression guards.
3. **Methods / struct-field / tuple-element fn values** (the `callee_param_is_fn`
   mirrors) — extend the box producer to those positions.

## 7. Tests

`internal/e2e/self_host_closure_arg_ir_test.go` (new), plus the existing
`arrow_lambda_test.go` and the self-host arrow-lambda IR test as regression
guards. The Stage-2 fixpoint (`TestSelfHostFixpoint` /
`TestSelfHostStage2FixedPoint`) must stay byte-identical at every phase (expected,
per §3).

## 8. Closure-call census (2026-08-17) — and why `defunctionalise` is not built

#6638 tracked porting native's IR optimiser passes to the self-host, one of
which was `defunctionalise`: rewrite an indirect closure dispatch into a direct
call when the target can be proven. Before building it, the population it would
rewrite was measured.

`irlower_run -clocensus` counts a module's env-first dispatches — the
`load_local S ; const_i32 0 ; arr_get 32 ; call_indirect(argc+1)` tail §2
describes — over the ops the backends see (lift_lambdas, then `lower_module`),
and splits them by where the env box comes from, since that decides how much
analysis resolving the site would need. Over the conformance corpus, the stdlib,
and the self-hosted compiler's own 91 modules:

| corpus | lowered fns | ops | env-first sites |
|---|---:|---:|---:|
| conformance (479 cases) | 994 | 46,820 | 23 |
| stdlib (64 modules) | 2,095 | 97,057 | 68 |
| self-host (91 modules) | 3,493 | 391,373 | **0** |
| **total** | **6,582** | **535,250** | **91** |

(The self-host row is measured a module at a time with no import resolution, so
1,484 of its functions bail out of the lowered subset and contribute no ops. The
zero above is therefore over 70% of that corpus, and the source-level check
below is what settles the rest.)

91 sites in 535k ops is 0.017%, and the split says the pass would not reach most
of them:

| provenance | sites | what resolving it needs |
|---|---:|---|
| `env_param` | 73 | whole-program: specialise the callee per call site |
| `env_call` | 8 | read one closure-returning callee's returns |
| `env_other` | 8 | points-to analysis |
| `env_local` | 2 | nothing — the op stream names the target |

Two findings decide it.

**The locally decidable case is already handled.** `env_local` is 2 because
`try_lift_binding` direct-calls a lambda that is only ever called, so it never
reaches an indirect dispatch at all. The 2 residual sites are a lambda that
escapes *and* is called locally, where the escape blocks the direct call. A
`defunctionalise` pass would be built to win those two sites.

**The self-hosted compiler contains no closure calls at all.** Not a lowering
artifact: across 91 modules its sources declare exactly two fn-typed parameters
— `astwalk.fern`'s `fold_expr` / `fold_stmt`, which nothing outside that module
calls — and bind no lambdas anywhere. The largest Fern program in existence
would gain nothing.

The remaining 73 `env_param` sites are the stdlib combinators (`array.map`,
`option.map`, `result.map`, `sort`, `test`). Devirtualising those means
specialising each combinator to its callback — callee specialisation or
inlining, a different pass from defunctionalisation, and one whose case would
have to be made on its own numbers.

So the row is closed as measured-not-worth-building rather than deferred. The
census driver mode and `internal/e2eselfhost/self_host_closure_census_test.go`
stay, which is what keeps the decision honest: the bucket test fails if
`try_lift_binding` stops direct-calling called-only lambdas, and the population
sweep fails if Fern code starts dispatching through closures at a rate this
conclusion was not measured against.
