# The stack of lift_impl waits on token fields

2026-11-25. x86-64. No change to the compiler. This entry corrects the next
lead of `2026-11-24-a-a-phi-of-literals-is-a-literal.md`.

## The lead that was wrong

That entry says that `phi_input` makes a value a twin of itself only when
the value is a parameter. `stack` in `ssa_lift.lift_impl` is a loop phi, so
the entry names that rule as the cause of the unproved `stack`.

A test of the lead: `phi_input` made each tracked box that is not the phi
itself a twin of itself, and `keyed` marked the box. The census did not
move: `x86_64/fern_main.unproved` stayed at 937, and `lift_impl` stayed at
52. `phi_input` makes a twin only of a plain count. At its joins, `stack`
(`v4198`) is free, free or 1, or maybe. It is never a plain count.

## Where the maybe comes from

`stack` starts as a literal (`v4078 = agg_lit &arr32|`). The loop then meets
that literal with the result of a push, so `stack` is free or 1. The maybe
comes into it round the loop. The joins that first make a maybe from inputs
that are not maybe are of two kinds.

A. A field of a struct result, read under a reuse token. In
`v7067 = call ssa_lift__lift_prod_mem ... v4198 ...`, the token is
`v7071 = phi v7067 v7070`. The field `v7072 = ld_at 8 rc v7067` is retained
only where the token is zero (`b717`). At the join `b719`, it holds 0 units
on one edge and 1 on the other.

`v10187 = ld_at 8 rc v10181` has the same shape, and it feeds `stack`
directly: `v53184 = phi v10187 v50674` is the input of the back edge.

B. A loop phi other than `stack`. `v4196` is free or 1, and the phis
`v7533`, `v7608` and `v7842` meet it with an input of 0 units.

The walks under the value of a token settle one token at a time. `stack`
gets maybes from several tokens round the same loop, so no single token
settles it.

## A trap

`join_key` refuses a word that is a token. In a test, `join_key` took a
token, so a field of shape A got a twin of the token at the join. The
compiler then refused `checker__qualify_at`: "spent without a unit of v644
(borrow invalid)". The certifier without the change passes that function,
so the refusal is false.

A twin keyed on the token tells which edge holds the unit. It does not tell
that the box still holds the unit of the field where the token is the box.

## How to read the walk

The certifier walks the function after `rcccompact.compact`, so its value
ids are not the ids of `FERN_DEBUG_RC_CERTIFY_DUMP`. `dense.original` in
`certify` maps a compact id to the id of the dump. The `at=` ids of the
census line are already mapped.

## Next lead

Model shape A. Where the token is the box, the box keeps the unit of the
field until `__fern_alloc_reuse` or the release of the box. Give the field
a twin of the token that tells this. Then the reuse or a test of the token
decides the twin. Shape B does not depend on A, so examine B first if A is
too large.
