# 2026-10-23 — a member leaves its record at the update

`ssaunits.twin_holds`, `takes_past_holds` and `slot_held`, the donor rule in
`hold_at`, and `semsink.spends` and `field_kept_apart`. Refs #12887.

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

- **A live child does not force a merge of a field read.** `reuse` also
  merged a second read when a child of the first was live out of the block
  (`child_live_out`). `evs` is such a child. Now `field_kept_apart` keeps
  a record field read apart when only its own block uses it. The plan can
  take it there. Other reads merge as before, and both
  `Streams.__stream_data` (`taken.list`) and `rbtree.ins` (its payloads)
  need those merges.

- **The donor is not a source that dies unconsumed.** At the update, the
  dying rule in `hold_at` held the first session read, because a read off it
  (an event) is consumed later. But the update consumes its donor, and the
  donor rule already holds the reads used past the update, here `evs`. The
  dying rule now skips the donor.

- **A twin's children are held.** The second read of `cur.session` can be
  taken only if no read off the first read is still in hand past it.
  `twin_holds` holds each such read (`acts` before its loop, `evs`), and
  `takes_past_holds` asks the takes again over the chains the holds cut.
  `slot_held` refuses a take whose slot a held read still loads. A take out
  of a read that `slot_held` refused is refused too: `units_of` accepts a
  take only when its source is owned, and repeats until nothing changes.

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

`TestSelfHostSecondTakeNeedsItsSource` pins the owned-source rule. Without
it, `emit`'s next call reads an emptied slot of a list, and the x86-64 build
crashes.

## Traps

A semsink merge rule that helps one function can cost another one a take.
Three rules were tried before `field_kept_apart`:

- With `child_live_out` removed from both `reuse` and `read_above`,
  `__applied` improved and `__stream_data` lost one take.
- A merge whenever the repeat is live out of its block kept
  `__stream_data`, but it re-read each payload of `rbtree.ins` in every arm.
  The `perceus_rbtree` rows grew by 16% in `.text` and 17% in instructions
  on aarch64.
- That rule also made `semsource.iterate_array` read `m = s.values[it]`
  again from the produced value after `s` went into `map_column`. With the
  inliner off, the plan for it does not certify. #13045 holds that failure.

Compare the census row by row and the perf rows, not only the mean.

`make build` does not run the new planner on the compiler. `bin/fern-selfhost`
is stage1, and the pinned stage0 builds it. Only a stage1 compile of
`compiler/fern.fern` does that, and the test harness also makes one with the
inliner off (`FERN_SEM_INLINE=`).

The certifier passes a plan that moves an element out of a list that another
holder still reads. `asm_ir.ssa_block_inst` held its first read of
`blk.insts`, and `slot_held` refused the re-reads of that field. But the
element takes out of those re-reads went in. `insts` is unique to its block,
so each take emptied the slot in place, and the next call crashed.

Only a stage2 compiler showed it: the stage1 build compiles, certifies and
passes the fixtures. Build stage2 with stage1 and compile a program with it.
