# 2026-10-06 — the unit planner keeps one live row per function

Self-host rc planning, every target. Refs #8171.

## The shape

`ssaunits.plan_analyzed` walks each reachable block backwards with a row of
booleans, one per value in the function, marking what is live. For every
block it built and scanned rows of that length three times over:

- `live_out_row` started each block from a copy of the all-false row and set
  the block's live-out values in it: 53,685 copies on `checker.fern`, about
  3,800 instructions each.
- The block-entry step and the return step each built another such row of
  dead values and passed it to `choose`, which scanned the whole row to list
  the values set in it: 60,440 scans, 190 M instructions.
- `reached_past_take` built the live-out row only to scan it for the values
  set in it.

Now `plan_analyzed` keeps a single row for the whole function. Each block sets
its live-out values and its terminator's uses, records every value it marks,
and clears those values again once the block's steps are chosen. The entry and
return steps list their dead values by id and go to `choose_ids`, and
`reached_past_take` walks `ssalive.live_out_ids` directly. `live_out_row`,
`add_use` and `choose` are gone.

`choose_ids` gives the same step as `choose` for the same set of values,
whatever order they come in. A value moves only if it is a member of the list,
and drops are kept in ascending order. The one new requirement is that each
value is listed once, so `use_ids` removes duplicates from a terminator's
value and its dependencies.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at 1dcfe2703 against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,004 rows:

| | main | one row |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 19.971 G | 19.587 G (−1.92%) |
| `ssaunits.plan`, inclusive | 1,425 M | 1,087 M |
| `__fern_arr_slice`, self | 292 M | 97 M |
| `ssaunits.choose`, self | 190 M | — |
