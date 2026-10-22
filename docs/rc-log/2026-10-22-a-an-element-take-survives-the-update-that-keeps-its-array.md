# 2026-10-22 — an element take survives the update that keeps its array

`ssaunits.needed_past_slot`, `ssaunits.kept_store_back`, `semdead.prune`.
Refs #12887.

## The shape

A record holds a list of records. A function reads one element, updates a
buffer in it, and puts it back:

```
let st: St = s.list[i];
let buf: u8[] = st.send.buf.concat(data);
return put(refuse(s, ""), i, St { ...st, send: Send { ...st.send, buf: buf } });
```

`refuse` is `Streams { ...s, refused: why }` and `put` is
`Streams { ...s, list: s.list.with(i, st) }`. `std/quic/streams.write` has
this shape, and so does `std/http/h3.__on_request`.

The element's take needs the array it came out of to be reached no further
except through the store-back at the same index. Here the array goes whole
into `refuse`'s update first, as the field the update keeps, and the
store-back is through the new record's field. `needed_past_slot` saw a use
of the array that was not the store-back and refused the take. The element
was then borrowed, its two records and its buffer were shared with the list,
and each update copied all three: 602 boxes for 200 updates on the
reproduction, three a round.

With borrowed receivers the leaf inliner splices `refuse` and `put`. It takes
the intermediate record apart, so the store-back is on the array the element
was read out of. It leaves a `copy` of each field it read, which nothing
reads. The copy was still a use of the array, and refused the take the same
way.

## What changed

`needed_past_slot` lets an array element's take through a record update that
keeps the array in a field (`kept_store_back`). Only field reads and one
rebuild that names the record as its donor can reach that record in its
block. One read of the kept field is permitted. That read stores at the taken
element, and the rebuild carries the result. Or it keeps the array through
one more such update, four deep at most.

The record, the field read and the store-back are then vetted for the
anchored walk (`reaches_past_kept`). A value reached only through them does
not count as another reader of the slot. The slot is empty only between the
take and that store, and nothing reads it there.

`semdead.prune` drops a `copy` nothing reads, beside the constructions it
already dropped.

## Measured

The reproduction, 200 updates of one element's buffer with a four-byte chunk,
under `FERN_LEAKCHECK`:

| receivers | before | after |
|---|--:|--:|
| `own` (the update kept as a record) | 611 allocations | 12 |
| borrowed (the update spliced apart) | 611 | 12 |

The shared case keeps its element: a holder of the first `Streams` reads an
empty buffer after three updates of another. Pinned on arm64, x86-64 and wasm
by the `element-take-survives-a-keeping-update` and
`element-take-through-spliced-update` rows of `TestSelfHostPayloadTakeIR`.

`TestSelfHostServeAllocsPerRequestH3` reads 224.92 per request, from 226.92.
Its ceiling is 226. The multiplexed row did not move (130.54, from 130.25).

## Next

`h3.__on_request` still copies two boxes a request. Its `r.fin || fin` puts a
branch between the element read and the store-back. `semsink` re-reads
`cur.reqs` in the join block, where `cur` dies. The re-read is a second read
of the field the take nulls, so the field's take is refused, and the
element's with it. A read dominated by a live read of the same slot must
reuse it rather than read again.

`FERN_SEM_DUMP=<function>` prints the semantic graph as the planner reads it,
with the parameter modes. `FERN_RC_CERTIFY_DUMP` prints the lowered one. A
`*` at the end of the name selects every function with that prefix.
