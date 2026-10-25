# 2026-10-25 — a borrowed call leaves its record to be read again

`semsink.sink_past_lent`, `semsource.with_lent_rereads` and
`semlower.inferred_rows`. Fixes #13262, found on the HTTP/3 hello path.

## The shape

`std/quic/streams.Streams.__on_stream`, reduced:

```fern
let st: Stream = cur.list[i];
if let Some(e) = cur.__credit_error(st.recv, end, frames.STREAM) {
  return cur.__fail(e);
}
...
next = Streams { ...next, list: next.list.with(i, next.list[i].__received(d, end, more, true)) };
```

`next.list` folds to the read of `cur.list` above the check. The take of
that read out of `cur` is refused where it stands, because the call
after it lends `cur` and may read the list. The store-back below is the
only place the list can leave `cur`, but semsink did not read the list
again there: it counted every call as spending its arguments, so `cur`
looked spent in the read's block. The element was then borrowed, and the
update copied the stream and its receive half on every STREAM frame.

## What changed

semsink's re-read rule is unchanged where it runs, before ownership
inference. Then a parameter declared borrowed may still be one the callee
consumes. `__absorb` in `std/tls/handshake` is one: counting its call
as a borrow there cost the TLS hello a take of `r.server`, one box a
request (42.00 to 43.00).

`sink_past_lent` runs after inference. A direct call leaves a record or
variant argument unspent when its callee's inferred contract borrows it,
and a projection read above such a call is read again below it. It makes
only the re-reads a lent argument allows: one the old rule would also
make on the same graph stays out. `with_lent_rereads` applies it to each
row, and `inferred_rows` replans each row whose graph moved.

## Measured

| Row | Before | After |
|---|---:|---:|
| `TestSelfHostBorrowedCallLetsTheSlotMove`, 1,000 updates after a borrowed call, x86-64 | 2,000 | 0 |
| `TestSelfHostServeAllocsPerRequestH3` | 102.93 | 100.93 |
| `TestSelfHostServeAllocsPerRequestH3Mux` | 65.98 | 63.95 |
| `TestSelfHostServeAllocsPerRequestTLS` | 42.00 | 42.00 |

## Traps

Declared modes are not final modes. A rule that relaxes "a call spends
its arguments" before inference must hold for a callee that inference
later counts, and `can_hold` on the callee's result is not that test:
`Result[Absorbed, HandshakeError]` passed it for a `Conn` the callee
returns.

Inference counts only record and variant parameters
(`ssasem.owned_shape`). A borrowed string may be one the callee keeps:
`C.on_event(e)` stores `e`, and leaving it unspent re-read the session
in `TestSelfHostKeptReadPastUpdate`'s loop, 2,000 boxes where it pins 0.

A second run of the re-read pass is not idempotent. semcse and semdead
run between, so the old rule finds re-reads on the second graph that the
first pass did not make. That also cost the kept-read row 2,000 boxes
until the pass made only the re-reads a lent argument allows.
