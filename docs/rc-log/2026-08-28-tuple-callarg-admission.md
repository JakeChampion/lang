# The tuple call-arg row was the same admission gate, one predicate along

`tuple_mixed__callarg__stored_struct` flips **leak → clean** on both
architectures. Like step 1, it needed neither of the things the plan
scoped for it.

## What the plan called for

`2026-08-28-tuple-callarg-instruments.md` ordered this row as steps 2-3: a
`"TCNT:"` counted tier in `param_counted_of`, folded by
`borrow_reg_with_counted`, and then routing that told **both** escape
scans, since `rctuple_esc_expr`'s call arm consults only `"TUPB:"`.

Neither was built. Nothing in `param_counted_of` or either escape scan
changed.

## What it actually was

`emit_struct_field_drops` already routes a direct tuple field to
`emit_struct_tuple_field_drops`, which is a full deep free: null-guarded on
the struct box, `__fern_rc_is_unique`-gated on the child walk, with the box
dec outside that gate. The mechanism was complete.

What was missing is that `struct_has_reclaim_array_field` — the predicate
deciding whether a struct is in the reclaim set at all — had no tuple case.
A struct whose ONLY rc field is a tuple was never admitted, so its drops
were never emitted and that finished code was unreachable. One clause
admits it.

So the tuple field's leak was, twice running, an admission predicate that
predated tuple fields rather than missing machinery. Worth carrying into
steps that remain: check what the routing already reaches before building a
tier for it.

## The retain, restored on the code's own terms

Step 1 shipped without the construction-side `fav_alias_inc`, on the
grounds that the emitted binary was byte-identical with and without it. It
was — because the drop it pairs with was unreachable. Making the drop
reachable makes the retain load-bearing, and
`emit_struct_tuple_field_drops` says so in its own comment: the
`is_unique` gate is sound only because "a non-fresh tuple field was
alias-inc'd at construction (the ExprStructLit tuple arm, gated on this
same routing predicate)". Without it two owners each read unique and the
second child walk frees what the first released.

It is restored here, alongside the admission that makes it matter. The
lesson generalises: "I cannot measure a difference" is evidence about the
current reachability of a path, not about whether a line is required.

## Measurement

| | x86-64 | arm64 |
|---|---|---|
| `callarg__stored_struct` before | clean / leak, 6 = 6 | clean / leak, 6 = 6 |
| after | clean / clean, 6 = 6 | clean / clean, 6 = 6 |
| `structfield__local_store` (step 1) | clean / clean | clean / clean |
| `callarg__read` (guard row) | clean / clean | clean / clean |
| `elemret__payload_refused` / `__box_tier_only` | clean / clean | clean / clean |

134 cells per architecture, zero errors, zero failures. Stage-2 fixpoint
green.

`conformance/cases/struct_tuple_field_shared` covers the two shapes the
corpus lacked and that the leak cells do not reach: two live owners of one
tuple box, and a tuple parameter stored into a struct the callee returns.
The two-owner case is what pins the retain — without it the `is_unique`
gate is a double free, and nothing else in the corpus would say so.
