# A call spends only what its mode counts

2026-11-25. Each target. The change is in `semsink`. Refs #14440.

## The shape

```fern
@noinline
function new_data(s: Streams, i: i32, room: i32): Streams {
  let sent = s.list[i].sent;
  let w = written(s.list[i]);
  ...
  let out = append_range(s.filled, s.list[i].buf, at, at + n as i32);
  return Streams { ...s, filled: out, list: s.list.with(i, sent_of(s.list[i], n, sent + n == w)) };
}
```

`written` borrows the element. `sent_of` has `st.fin_sent || fin`, so its
splice puts a join between the update block and the `with`. The program in
`TestSelfHostLentThenTakeAcrossJoin` calls `new_data` 1,000 times.

## The cause

`semsink.reuse` merges a repeated read into an earlier read of the same
slot. It does this when a read off the earlier read is spent above the
repeat. It ran in `semsink.sink`, before ownership inference. At that point
a call spent each of its arguments. So `written(s.list[i])` was a spend of
the element.

`reuse` then replaced the update block's read of `s.list` with the read of
the entry block. The plan held that first read across the join. It made a
new `Stream` and copied the list on each call.

#16944 made `reuse` read the declared modes of the callees. The inference
can change such a mode later. `conn.Space.__received` borrows its receiver
before the inference and consumes it after. So that change shared an element
that a call consumes.

## The change

- `reuse` runs in `sink_past_lent`, after the inference, with the inferred
  modes. A call spends only the arguments that its callee counts.
- A merge can undo a re-read that `sink` made. So `sink_past_lent` runs the
  re-reads of `sink` again on a function that `reuse` changed.
- `field_kept_apart` keeps a record field read apart only when the parent of
  the read is not itself a read. The plan takes such a read past the live
  reads off its twin only when it owns their parent (`ssaunits.twin_holds`).
  It never owns a read.

The early `sink` keeps its re-reads. The `known` reads that `reuse` handed to
them are gone, with `reads_at`.

## Measured

`__heap_alloc_count()` over 1,000 calls of `new_data`, `-O`:

| Target | main `e0ada713` | with the change |
|---|--:|--:|
| x86-64 | 2,550 | 850 |
| arm64 | 2,550 | 850 |
| wasm32-wasi | 2,550 | 850 |

850 is the count of the same program with `fin_sent: fin`, which has no
join.

The HTTP/3 probes, allocations a request, x86-64:

| Probe | main | with the change |
|---|--:|--:|
| `TestSelfHostServeAllocsPerRequestH3` | 49.69 | 49.69 |
| `TestSelfHostServeAllocsPerRequestH3Mux` | 28.67 | 28.48 |

## The traps

Each of the three parts has a test that fails without it:

| Without | Test | Result |
|---|---|---|
| the move of `reuse` | `TestSelfHostLentThenTakeAcrossJoin` | 2,550 allocations |
| the second run of the re-reads | `TestSelfHostServeAllocsPerRequestH3` | 52.56 a request, ceiling 50 |
| the parent rule of `field_kept_apart` | `TestSelfHostRereadPastAncestorSpend` | exit 2: each flush copies |

The second row is `Streams.on_stream` of `std/quic/streams`. The early `sink`
re-read `next.list[i]` in the update block. The late `reuse` merged that
re-read into the first read. A read off the first read lives out of the
block. The census (`TestSelfHostServeAllocsCensusH3`) put the +2.88 in
`on_stream`: one `__fern_arr_slice` and one box from `Stream.__received`.

The third row merged on main only because the early `reuse` counted the
borrowed call `Q.__marking` as a spend. Its twins read `v13`, a read off a
record that the function builds. `twin_holds` does not hold the reads off
such a twin, so a kept-apart read copies.

A driver that calls `Streams.on_stream` alone does not show the second row.
It allocates 2,000 with each compiler. Compile the HTTP/3 server of the
probe (`e2eharness.ServeAllocsH3ServerSource`) with `FERN_DEBUG_SEM_DUMP=on_stream`
to see the shape.

## Next lead

`streams.Stream.__sent` was the second name of the splice search in #14440.
With this change, splice it alone again, and look at the census.
