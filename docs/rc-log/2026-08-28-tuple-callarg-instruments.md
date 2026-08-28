# Instrumenting the tuple wave — and a gap the instrument found

Setting up to route the tuple release families through the plan, the first
step was an instrument: a cell the routing would move. Measuring it first
changed what the wave looks like.

## The planned instrument does not discriminate

`tuple_mixed__callarg__read` — a tuple local passed to a read-only callee —
was the obvious analogue of the cell the struct wave flipped. It measures
**clean / clean** today. The credit gate carries its own interprocedural
borrowability tier, so it already admits a call arg at a borrowable
position; there was never a refusal there to lift.

That makes the row a **guard**, not a gain: it pins the behaviour the
routing must not regress. Kept for exactly that.

## The shape that does discriminate is a new gap

`tuple_mixed__callarg__stored_struct` — the callee KEEPS the tuple, in a
struct literal it returns — measures **clean / leak** on both
architectures, with exits agreeing (6 = 6) and the underflow counter at 0.

The store is a COUNTED construction, so native's caller-side release is
balanced and it frees `keep`. The self-host's credit gate reads any call
arg at a non-borrowable position as an escape and refuses, leaking the box
and its element.

This shape was not in the enumerated matrix, so the 08-28 elemret entry's
"actionable-gap count is zero" was true of what was enumerated, not of the
family. It is one row again.

## What that does to the wave's shape

The tuple routing is therefore not a no-op convergence change: it has a
real row to gain. But the row it gains is the counted-sink channel, which
is the one the struct wave needed `struct_arg_to_handback` and a
`handback_params` registry for — the plan grants a call arg unconditionally
and cannot see what the callee did with it. Granting this shape is correct
only because the store is counted, and consuming the plan's verdict is
sound only once the self-host's struct-literal store is proven to retain a
tuple field co-extensively (the #7253 discipline).

So the next increment is not "route the gate". It is: establish the
counted-store retain for tuple fields, then route, with this pair of cells
as the instrument — the guard row must hold clean while the gap row flips.

## Also recorded

Nothing in the compiler changed here. Both rows were measured with
`FERN_LEAK_MATRIX_DUMP=1` on x86-64 and arm64 and pinned at their measured
verdicts.
