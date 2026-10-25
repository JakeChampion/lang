# 2026-10-25 — a lent view holds its array, so its record can move

`ssaunits.outliving` and `lends_view_of`. Found on the HTTP/3 hello path
(branch `h3-hello-allocs`).

## The shape

```fern
s = step(s, s.buf[0:8]);
```

`step` consumes `s` and borrows a view of `s.buf`. The view `v19` is
anchored on the field read `v16`, and `v16` on `s` (`v12`), so at the call
`lent_owners` found `s` behind a lent operand and lent `s` instead of
moving it. The callee then saw a shared `s` and copied its record and
every array and record below it that it updated.

`hold_at` already holds a field read that is itself an argument of the
consuming call (`has_id(args, w)` in `outliving`). It now also holds one
that a lent view argument is anchored on. Holding `v16` retains the array
for the call, `through_takes` cuts the view's chain at the hold, and `s`
is no longer a lent owner, so it moves.

The rule is limited to view arguments (`ssasem.holds_view`). Taking any
anchored argument held the ACK-range space in `conn.Conn.__received` as
well and cost the HTTP/3 hello two allocations a request
(116.93 to 118.93).

## Measured

| Row | Before | After |
|---|---:|---:|
| `TestSelfHostLentViewLetsItsRecordMove`, 1,000 calls through the field, x86-64 (arm64 is 0 after too) | 4,000 | 0 |
| the same through a local holding the array | 0 | 0 |
| the same, wasm32, where each lent slice boxes its descriptor (#13207) | not measured | 1,000, as through a local |

std/serve had read its QUIC receive buffers into locals to stay out of
this shape (`__h3_event`, 177.92 to 159.30 allocations a request). With
the hold the loop reads them through `cur.quic` again at the same count,
116.93.

## Traps

`ssasem.is_view` is true only for a string view. An array view is
`holds_view`; the first version of the rule asked `is_view` and held
nothing.
