# 2026-10-30 — a list read to reach a held element is borrowed

`Streams.__signals`, `__stream_windows` and `__stream_data_blocked` scan the
stream list of a record that the loop rebuilds:

```fern
let st: Stream = cur.list[i];
if (st.reset_state == TO_SEND) {
  cur = cur.__add(room, ...);
  ...Stream { ...st, reset_state: SENT }...
}
```

`st` is read after the call that consumes `cur`, so the plan holds it. But
each iteration did more than retain `st`:

1. It retained `cur.list`.
2. It tested whether the list was unique, to take `st` out of it. The list
   had just been retained, and `cur` still held it. Thus the test could never
   pass, and the shared arm retained `st`.
3. It released the list with a second test.

That is about 30 instructions for each stream on every scan. The server scans
all 64 streams for each packet that it builds.

## The change

- **No take past a hold that cannot pass.** `takes_past_holds` lets a read
  be taken out of a held source. The read now becomes a take only when no
  container that the source is anchored to is live past the read
  (`anchor_live_at`). The liveness here counts each value's own uses only.
  A live container still counts the source, so the source cannot be unique.

**A second hold pass.** `held_elements` holds a read that is in hand where
its source is consumed. The first pass counts a read in hand through the
dependency chains. Thus `cur.list` was in hand because `st` is anchored to it.

The second pass sets `Flows.firm` to the holds of the first pass. It counts a
read in hand through its own uses. It also counts the anchored reads that the
first pass did not hold. Thus `cur.list` is not held, because `st` has its own
hold.

A held read can be read after the consumption. That read is an own use of its
container, so the container stays held.

The loop head is now one retain of `st`, and the arm that does not change the
stream releases it.

## Measurement

The HTTP/3 hello server, 20,000 requests with 64 in flight, x86-64, `-g`,
callgrind on the server:

| | instructions for each request | `__signals` | `__stream_data_blocked` |
|---|--:|--:|--:|
| a length set keeps its array sole | 24,306 | 341 | 327 |
| a list read to reach a held element is borrowed | 23,597 | 154 | 136 |

## Tests

`TestSelfHostScanHold` scans a list in this shape. The x86-64 leg pins 1
uniqueness test (the reuse of the element's box for the rebuilt element) and
2 retains in `scan`. Before the change, `scan` had 3 tests and 3 retains. The
program runs with a unique table and with a shared one. It runs under the leak
census, and on arm64 and wasm.

## Next lead

The hold still retains `st` at its read, on every iteration. Only the arm
that consumes `cur` needs the count. A hold placed at the consumption can
make the scan free of count traffic.
