# 2026-10-30 — a read is held only on the arm that consumes its record

A loop that scans `cur.list[i]` and consumes `cur` on one arm holds the
element. The plan retains a held read at the read and releases it at its
death. Thus every iteration paid a retain and a release, also when the arm
that consumes `cur` did not run.

```fern
let st: Stream = cur.list[i];
if (__held(st) && st.sent >= st.send_max && st.blocked_at != st.send_max) {
  cur = cur.__add(room, frames.StreamDataBlocked(st.id, st.send_max));
  if (cur.fit) {
    cur = cur.__put(i, Stream { ...st, blocked_at: st.send_max });
  }
}
```

## The change

`semlatehold.fern` runs after ownership inference, from
`semsink.sink_past_lent`. It looks for a call that consumes a value `r`, and a
read `v` off `r` (up to four record, tuple or array reads deep). These
conditions must be true:

- `v` is read in a block that dominates the call, but not in the call's
  block.
- `v` is live past the call.
- No phi takes `v`. A phi needs a count on every arm, so the hold at the read
  costs no more there.

The pass reads `v` again from `r` just before the call. The uses of `v` that
the call dominates take the new read. Then the pass checks that `v` is not
live past the call. If `v` is still live, the pass removes that candidate and
tries again.

The first read is then a borrow. The plan holds the new read, because it is
in hand where `r` is consumed. Thus only the arm with the call retains it.

## Measurement

The HTTP/3 hello server, 20,000 requests with 64 in flight, x86-64, `-g`,
callgrind on the server:

| | instructions for each request | `__stream_data_blocked` |
|---|--:|--:|
| a list read to reach a held element is borrowed | 23,597 | 136 |
| a read is held only on the arm that consumes its record | 23,393 | 73 |

`__signals` does not change. Its `st` is assigned again in the first arm, so
a phi takes it.

## Tests

`TestSelfHostScanHold` now also pins that `scan` has no retain before the
test of the element's state. Before the change, the retain came first. The
program adds `scan_len`, which reads the element after the call in the
call's own block. The program runs each function with a unique table and
with a shared one, under the leak census, and on arm64 and wasm.
