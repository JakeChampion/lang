# The knockout said delete it; the knockout was wrong

`struct_routes_field_reclaim_at`'s `struct_has_deep_tuple_field` clause looked
redundant. For a few hours it genuinely was, in part: `061cdeb` had added a
tuple case to `struct_has_reclaim_array_field`, which the outer predicate
consults first, so the second clause only ever ran on what the first refused.

That first clause has since been retracted (`80730c8`, on the grounds that the
row it claimed to close was already closed by the `"TCNT:"` tier). So this
clause is now the only tuple admission — but the measurement below was taken
while both existed, and its lesson does not depend on that.

## Every gate said the clause was dead

Knocked out, and re-run across everything that normally decides this:

- both leak matrices re-dumped — **zero rows moved, either architecture**;
- tuple, rc-tuple, construction-retain, container-sink, feature census —
  **zero failures**;
- arm64 stage-2 fixpoint — green.

Not one gate dissented. On that evidence the Erasure rule says delete it.

## Deleting it would have reintroduced a 12800-byte leak

The predicates were never nested. `is_leaksafe_tuple_field` is TYPE-only — no
`structs` view — so it cannot classify a struct-typed element, and the code
said so outright: "Struct- / enum-array elements need the `structs` view to
classify, so a tuple field carrying one still bails."
`tuple_field_deep_droppable` does take `structs` and admits a reclaim-struct
element via `struct_has_reclaim_array_field(P)`.

`Holder { t: (i32, P), … }` with `P { xs: i32[], k: i32 }` is the shape only
the second admits:

| tree | census | exit |
| --- | --- | --- |
| clause live | `allocs=400 frees=400 live_bytes=0` | 23 |
| clause removed | `allocs=400 frees=100 live_bytes=12800` | 23 |

Both oracles exit 23 either way, so only the census separates them — and no
corpus in the gate set runs this shape.

## The asymmetry

The knockout is the method used all week to prove a clause load-bearing, and
here it returned a confident false negative with every gate agreeing. The two
outcomes are not symmetric:

- a knockout that **moves** something proves the clause is load-bearing;
- a knockout that **moves nothing** proves only that the corpora do not reach
  it.

Treating the second as proof of deadness is how live code gets deleted. Where
the question is "is this clause dead", the corpora cannot answer it — read what
the clause admits that its neighbour does not, build that shape, and measure.

`TestSelfHostTupleStructElemFieldX86_64` now pins it, so the next knockout
moves a number instead of reporting silence.
