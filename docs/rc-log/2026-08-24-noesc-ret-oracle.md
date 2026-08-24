# The returns-no-param-escape subset oracle

The named burn-down from the Option-family routing: `rc_fe_rhs_tainted`'s
user-call arm tainted a call result on ANY tainted argument, and every
non-own param is taint-seeded regardless of type — so `var v = mk(r)` with
a scalar `r` was plan-tainted through its own init while native untaints
it via `returnsNoParamEscape` (#4357's rule).

`noesc_ret_fns_of` is a strict SUBSET port of native's oracle: an
optimistic knock-out fixpoint over the module where a function stays
qualified while every return expression is built from literals,
scalar-typed non-shadowed params, arithmetic over those, nullary variant
idents, and calls to still-qualified callees (builtin `Some`/`Ok`/`Err`
and user variant ctors seeded) — no fresh-locals recursion, no cow
chains, so every name this admits, native admits too: the dump gate can
only converge, never newly diverge (it held with zero pin movement on the
corpus). Rides FnSigs as `noesc_ret_fns`, built once per module.

## What it does and does not retire

With the oracle, the plan half of the Option-family union gates now
grants the call-provenance shapes on its own. The credit half still
CANNOT retire: `binder_forin_collide` is structurally name-level — a
for-in binder colliding with a credited binding's name taints the shared
name, and no name-level verdict can grant one site and refuse the other.
Retiring the credit halves needs PER-SITE plan verdicts, which is the
promotion's keying question, not this oracle's. Measured: plan-only with
the oracle passes the call-provenance suites and fails exactly the
name-collision one — the union stays, with its retirement precondition
now precise.
