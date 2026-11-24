# A comparison reads its operands

The sink pass counted a string comparison as a spend of its operands. So a
later update of the records above the string did not read them again. The
plan then copied the records (#14440).

## The shape

```
function readable(own c: Conn): Conn {
  if (c.quic.streams.refused != "") {
    return c;
  }
  return Conn { ...c, quic: Q { ...c.quic, streams: St { ...c.quic.streams, n: c.quic.streams.n + 1 } } };
}
```

The condition reads `c.quic`, `c.quic.streams` and the string `refused` in
the entry block. The update block reads `c.quic` again.

## The cause

`semsink.spends` gave true for each operation that is not a projection, a
variant test, a length or a phi. A binary `!=` on two strings was thus a
spend of `refused`. `semsink.reuse` then found a child of the first read of
`c.quic` that was spent above the update block (`child_used_above`). So it
replaced the second read of `c.quic` with the first read. The plan takes a
field only under the test of its parent in the update block. With the first
reads kept, it made a new `St` and a new `Q` on each call.

A comparison of two integers does not show the cost, because an integer
field is not a counted read. A string one record down (`c.quic.why`) does
not show it either.

## The fix

`spends` takes the instruction. A binary operation whose operator is a
comparison (`ssasem.comparison`) is not a spend.

## Measurements

`__heap_alloc_count()` over 1,000 calls of the shape above, x86-64:

| Build | Allocations |
|---|--:|
| main `cf3b2a78` | 2,001 |
| with the fix | 3 |

`TestSelfHostCompareThenTake` holds the shape on the host targets. On main
it exits 2 on x86-64 and arm64.

The HTTP/3 probes of `TestSelfHostServeAllocsPerRequestH3`, allocations a
request. "Each method spliced" is a local patch that admits each method of
more than one block into the splice (#13990). It is not committed.

| Build | serial | multiplexed |
|---|--:|--:|
| main | 49.69 | 28.67 |
| main, each method spliced | 52.56 | 31.68 |
| fix | 49.69 | 28.51 |
| fix, only `h3.Session.on_stream` spliced | 49.69 | 28.67 |
| fix, each method spliced | 50.56 | 29.63 |

With each method spliced, a search over the 240 method names that the
patch admits named two names. Alone, `h3.Session.on_stream` cost 2.00 a
request and `streams.Stream.__sent` cost 2.87. Spliced into
`h3q.Conn.__readable`, `on_stream` put its join between the comparison
`cur.quic.refused() != ""` and the update of `cur.quic`. The census
(`TestSelfHostServeAllocsCensusH3`) showed two new boxes there, a
`streams.Streams` and a `conn.Conn`. The fix removes them.

## The next lead

`streams.Stream.__sent` still costs 0.87 a request when it is spliced, with
each method spliced. `st.fin_sent || fin` adds a join, and
`Streams.__new_data` spends the element in the join block.

## A trap

`FERN_CACHE_DIR` holds compiled units. A switch read from the environment
is not in the key of a unit. So a compile with a new switch can give the
same binary as before. Use a new cache directory for each such compile.

`semsource.spliceable_body` decides only `unnamed`: whether the lowering can
drop the body after the splice. A method that another module names is not
`unnamed`, so a change to `spliceable_body` alone does not splice it. The
gate of a splice is `seminline.splice_plan`.
