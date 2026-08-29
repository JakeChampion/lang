# The capture-RC family was never reachable, and emit-hash is what proves it

`clo_rc` (#4354) is deleted rather than re-keyed. It was on #7253's
highest-risk list — a NAME key under a first-match value lookup, the shape
`add_tup_elem_kinds` documents as the collision hazard — and the re-key was
scoped as the next slice. Measuring it first is what changed the answer.

## The measurement

Two instruments, from opposite ends, agreeing.

The instrumented count on #7253 (2026-08-27) reported, across the conformance
corpus:

| probe | fires |
|---|---|
| `clo_init` — the branch that builds a closure env box | 26 |
| `clo_rc_approved` returns true | **0** |
| `add_clo_cap_kinds` — the build-site kinds registration | **0** |
| the exit sweep resolving a kinds row | **0** |

`scripts/selfhost-emit-hashes` states the same thing without instrumentation
and over a wider surface: deleting the family leaves the emitted bytes
**identical on all 1551 (fixture, target) rows**, x86-64 / arm64 / wasm. An
emitter that fires anywhere in that corpus cannot come out pure, so the
byte-identity IS the reachability result — which is the useful part of this
entry, because it needs no temporary code and no reverted patch.

## Why it cannot fire

`try_lift_binding` is a whole-module AST pre-pass and it matches the same
syntactic shape the classifier does — a `StmtVar` whose init is an
`ExprLambda`. A capturing lambda bound to a var and only ever called is
param-lifted to `__lam_N(args, caps…)` and the binding statement is dropped,
so `clo_rc_candidate_names(fn.body, …)` in `lower_func` has nothing left to
collect.

The three decline conditions of the lift are near-disjoint from the
approval's, which is why the residue is empty rather than merely small:

| lift declines when | the approval |
|---|---|
| the name is a reassign target | refuses the same, against a superset |
| the name is used as a VALUE, not only called | that is an escape; `body_unsafe_for` refuses it |
| `cap_type` cannot type a capture | `cap_param_for` declines on the same test and takes the module off the IR path, so no `clo_init` binding is built |

## The sequencing rule this is an instance of

> **Before re-keying a fact family, measure that the family fires.**

A key migration's own failure mode is silent (#7253, 2026-08-22: a binding
that resolves *no* credit shows only as a leak, and `FERN_LEAKCHECK` reads
clean on both sides of an over-release). A migration whose only gate is a
suite that cannot go red is that problem one level up — the green is
unfalsifiable. `internal/e2eselfhost/self_host_closure_env_rc_ir_test.go` is
exactly such a suite: its four cases pass, and pass identically with the
family deleted, because the param-lift makes each capture an ordinary local
the `"STR:"` / array sweeps free. Same exit code, different mechanism, and no
reading of the result distinguishes them.

The cases are kept and their commentary corrected. What they actually gate is
worth gating: a regression that stops lifting one of these shapes puts it back
on the env-box path, where it leaks (98) or over-releases (99).
