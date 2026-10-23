# 2026-10-23 — a member leaves its record at the update

`ssaunits.twin_holds`, `takes_past_holds` and `slot_held`, the donor rule in
`hold_at`, and `semsink.spends`, `earlier_read` and `spent_under`. Refs
#12887.

## The shape

`std/http/h3q.Conn.__applied`, as entry
`2026-10-22-e-a-read-is-kept-past-its-records-update.md` left it:

```fern
let acts: h3.Action[] = cur.session.actions;
let evs: h3.Event[] = cur.session.events;
if (acts.len() == 0 && evs.len() == 0) {
  return cur;
}
cur = Conn { ...cur, session: cur.session.taken(), quic: __acts(cur.quic, acts, now) };
for e in evs {
  cur = cur.__session_event(e);
}
```

`taken` got a borrowed session, so it copied the session on each pass. Four
changes let the session leave `cur` at the update.

- **A `length` does not spend.** `semsink.reuse` merges a second read of
  `cur.session` into the first when a read off the first is spent. It counted
  `acts.len()` as a spend. So `taken` read the session from the first read,
  in the block before the branch, where nothing can take it. Now
  `semsink.spends` leaves out `length`, and the second read stays where
  `taken` uses it.

- **A live child does not force a merge.** `reuse` also merged a second read
  when a child of the first was live out of the block (`child_live_out`).
  `evs` is such a child. Now `reuse` merges when the second read itself is
  live out of its block. Then `sink_rereads` reads it again in the block that
  uses it. `Streams.__stream_data` needs that merge: `taken.list` is read
  where the update is made and used only at the final `with`.

  The merge made a re-read that `read_above_for` let through.
  `semsource.iterate_array` reads `m = s.values[it]`, gives `s` to
  `map_column`, and uses `m` after. The merge made `m` the first read, and
  sink then read it again from the produced value after the call.
  `read_above_for` checked the spends of the direct parent only. `reread`
  then read each parent again with that parent as its own leaf. Now
  `spent_under` refuses a re-read when any parent above it is spent where
  the leaf is still live.

- **The donor is not a source that dies unconsumed.** At the update, the
  dying rule in `hold_at` held the first session read, because a read off it
  (an event) is consumed later. But the update consumes its donor, and the
  donor rule already holds the reads used past the update, here `evs`. The
  dying rule now skips the donor.

- **A twin's children are held.** The second read of `cur.session` can be
  taken only if no read off the first read is still in hand past it.
  `twin_holds` holds each such read (`acts` before its loop, `evs`), and
  `takes_past_holds` asks the takes again over the chains the holds cut.
  `slot_held` refuses a take whose slot a held read still loads.

## Measured

| Row | Before | After |
|---|---:|---:|
| `TestSelfHostServeAllocsPerRequestH3` | 197.92 | 192.92 |
| `TestSelfHostServeAllocsPerRequestH3Mux` | 109.91 | 106.85 |

In the census (`TestSelfHostServeAllocsCensusH3`), the serial mean goes from
197.40 to 192.40. `h3.Session.taken` called from `__applied` (4.00 a request)
and `conn.Conn.events_taken` (1.00) are gone. The ceilings are lowered to 194
and 108.

`TestSelfHostKeptReadPastUpdate` pins the shape on x86-64, arm64 and wasm.
1,000 calls allocate no box, against 1,000 before. Its events are strings that
`on_event` keeps, so the event loop consumes them as `__session_event` does.

`TestSelfHostRereadNotThroughSpentParent` pins `spent_under`. Without it, the
re-read keeps the record alive past the call, and the call copies it: 30
calls allocate 250 boxes on x86-64, against 210.

## Traps

A semsink rule that helps one function can cost another one a take. With
`child_live_out` removed from both `reuse` and `read_above`,
`__applied` improved and `__stream_data` lost one. The list was read where
`taken` is built and kept to the final `with`. So the planner did not take
the stream out of the list. Compare the census row by row, not only the mean.

`make build` does not run the new planner on the compiler. `bin/fern-selfhost`
is stage1, and the pinned stage0 builds it. Only a stage1 compile of
`compiler/fern.fern` does that, and the test harness also makes one with the
inliner off (`FERN_SEM_INLINE=`). Only that compile showed the re-read through
`map_column`. Its plan does not certify, and #13045 holds that failure.
