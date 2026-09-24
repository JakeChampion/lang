# A record field read takes its unit

2026-09-24. Self-host, semantic lowering.

```
function scan_body(own s: State, line: string, ...): State {
    var ws: Words = s.words;
    s = State { ...s, words: s.spare };
    var pos: i64[] = ws.pos;
    ...
    while (...) { ... pos = pos.with(nw, wp); ... }
}
```

`coreutils/fmt.fern` copied `pos`, `meta`, `gaps` and `lines` on every
input line.

## Cause

`pos` was a borrow of `ws`, itself a borrow of `s`. The update's
receiver was not a unit of the frame's own, so it arrived as a retain,
the uniqueness test saw two counts, and the first `.with` of every call
copied the whole array.

An enum payload read could already take its unit
(`ssaunits.payload_root`, lowered by `ssarc.steal`): when the box dies at
the read and is reached after it only through its other slots, the slot
is nulled if the box is unique and the value is the frame's, and
retained as before if it is shared. A record field read now takes the
same way, for an array or record field. A string field stays a borrow,
since it may be a view or a literal at run time.

Two rules had to move for the fmt shape:

- A root that is itself a take counts as owned, so a read of a field of
  a record that was moved out takes in turn. `payload_takes` runs to a
  fixpoint.
- `live_out_has` on the root read the flow from before the take, in
  which everything the taken value anchors keeps the root live. For a
  record field `needed_past` asks instead whether a later block names the
  root, or a live-out value reaches it other than through this read or a
  read of another of its slots. An enum payload keeps `live_out_has`:
  under `needed_past`, `sibling-slot-survives-the-take` in
  `TestSelfHostPayloadTakeIR` freed none of its 43 blocks.

The trap this sets: a taken field is the frame's own unit from the take
on, and its record may already be freed. `hands` and `steals` treat a
projection as a field still in its record, gating or stealing it on the
record's count, so they skip a take. Before they did, a take handed to a
counted slot and read after the call gave the callee its only unit and
read it after the free, and a take lent to a call in a loop re-tested
the freed record's count round every call
(`TestSelfHostRecordFieldTakeHandedOn`, under the sanitizer).

## Measured

Self-host build of `fmt`, x86-64: 2.44 G to 0.83 G instructions over
3 MB of prose, and 412.2 ms to 263.8 ms over 6 MB (GNU 9.12: 124.8 ms).
Allocations over 300 KB of prose: 65,106 to 43,357.

`TestSelfHostRecordFieldTake` is `scan_body` in miniature, on each target
the host runs: one allocation per call, where it was two.
`TestSelfHostRecordFieldTakeAcrossBlocks` takes two fields after a loop
and reads them across later blocks, under the leak census.
