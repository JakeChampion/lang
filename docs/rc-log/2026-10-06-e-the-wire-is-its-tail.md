# The wire is its tail

2026-10-06: `serve.__serve_wire`, `__wire_file`, `__wire_chunks`,
`__serve_respond`, `__tail_keeps`. Slice 6 of
`docs/NET-P0-MESSAGE-LAYER-PLAN.md` §6.2 (#9853), the `__Wire` row.

## The shape

```fern
struct __Wire { tail: __Tail, keep: boolean }
let wire: __Wire = __serve_wire(c.out, method, version, resp, keep_alive, clk);
__serve_respond(drv, c, at, wire, opts, answered, tail, clk);
```

`__serve_wire` wrote the response into the loop's builder and handed back
a record of two words: the tail still to be produced, and whether the
connection may persist after it. One box per request, passed whole into
the respond path.

## The rule

The first cut returned the two as a tuple, for the pairing pass to return
in two words. It refused: the file-body path opens a reader and calls its
trait methods, indirect calls, and once a program has a park, a function
that reaches an indirect call may suspend and is never paired (the trace
showed the 48-byte tuple box still built in `__serve_wire`, and its body
carrying the suspension pass's saves). The same shape that holds
`__serve_produce`'s and `__serve_ready`'s tuples.

The second word was never information of its own. `keep` was the
`keep_alive` the caller passed in, except on the close-delimited chunk
tail, which ends the connection, and that tail is the one whose ending
string is empty. So `__serve_wire` returns the tail alone, and
`__serve_respond`, which now takes the method, version, response and
`keep_alive` and calls `__serve_wire` itself, derives persistence as
`keep_alive && __tail_keeps(rest)`. Nothing carries the two together.

## Measured

x86-64, `TestSelfHostServeAllocsPerRequest`, a hello handler on one
keep-alive connection:

| Step | Per request |
| --- | ---: |
| Before | 9 |
| The wire as a pair | 9 (refused: may suspend) |
| The wire as its tail | 8 |

The §6.1 trace at 9, resolved by symbol: two in `__request_head`, the
`Framed` box, two in `http.ok`, the wire's box, `__serve_produce`'s and
`__serve_ready`'s tuples, and the read's copy. The reactor wait's row was
already gone.

## What is left on the path

The read's copy, the parse's three, `__serve_produce`'s and
`__serve_ready`'s tuples, and the handler's response and header map: the
§6.1 rows less this one.
