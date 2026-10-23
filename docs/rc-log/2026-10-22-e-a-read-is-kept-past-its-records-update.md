# 2026-10-22 — a read is kept past the update of its record

`semsink.spent_by`, `child_live_out`, `projection_tree` and `twin_of`.
Refs #12887.

## The shape

`std/http/h3q.Conn.__applied`:

```fern
let acts: h3.Action[] = cur.session.actions;
let evs: h3.Event[] = cur.session.events;
...
cur = Conn { ...cur, session: cur.session.taken(), quic: __acts(cur.quic, acts, now) };
for e in evs {
  cur = cur.__session_event(e);
}
```

`semsink` read `evs` again inside the event loop, from the old `cur`. That
kept the old `cur` alive past the update that rebuilds it. So the update
could not use the old box, and each pass of the loop copied the whole
`Conn`. Three things let the re-read through.

- **The update's donor was not a use.** A record update names its donor in
  `imm`, not in its operands. `spent_by` counted only operands, so the
  update did not spend the old `cur`, and the re-read looked safe.

- **A live child did not stop a re-read.** `evs` lives on past the update.
  A take of a second read of its slot leaves `evs` reaching the record
  another way, so the planner refuses the take. Now semsink does not re-read
  a parent whose child is live out of the block (`child_live_out`). On the
  same condition, it merges a repeated read into the first one.

- **The two first reads were separate values.** `acts` and `evs` read
  `cur.session` twice in one block. `semcse` merges the two reads only after
  `semsink`, so the child `evs` hung off the second read and the check
  looked at the first. Children are now kept under the block's first read of
  their slot (`twin_of`).

## Measured

| Row | Before | After |
|---|---:|---:|
| `TestSelfHostServeAllocsPerRequestH3` | 200.92 | 197.92 |
| `TestSelfHostServeAllocsPerRequestH3Mux` | 112.91 | 109.91 |

The census (`TestSelfHostServeAllocsCensusH3`) reads 197.40 and 108.66. The
ceilings are lowered to 199 and 111.

`TestSelfHostKeptReadPastUpdate` pins the shape on x86-64, arm64 and wasm.
1,000 calls allocate 1,000 boxes, against 2,000 before.

## What stays

The member is still copied: `taken` gets the session read before the branch
that returns `cur` whole, so the planner cannot take the session out there.
The take must wait for the block after the branch. There the walked array
`evs` is a borrow tied to `cur`. To remove the copy, the planner must count
`evs` once. Then the session can leave `cur` at the update.
