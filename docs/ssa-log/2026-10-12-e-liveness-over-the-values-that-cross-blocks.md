# 2026-10-12 — liveness over the values that cross blocks

`ssalive.compute_checked`. Refs #8171. No emitted byte changes.

## The shape

`compute_checked` is the dependency-aware liveness the RC planner and its
analyses read. On the whole compiler's self-compile it was 1.55 G Ir of self
time, the third largest function, over 22 K calls from `ssadeps.analyze` and
`ssaunits.units_of`. Each block carried a bit row over every value of the
function, and each pass over a block merged its successors' rows word by word.
So the work grew with blocks times values, though most values are read only in
the block that defines them.

## What changed

A value can be live into or out of a block only if some block reads it, or one
of the values anchored to it, before defining it, or a phi edge carries it.
Every live bit starts at such a read and flows back from there. So the rows now
carry a bit only for those values. The scan that used to set use bits records
those reads instead, and the values they name get dense slots in ascending
order. `Flow.slot` maps a value to its bit, -1 for a value with none, and
`Flow.slot_value` maps a bit back. The readers (`live_in_has`, `live_out_has`,
`live_in_ids`, `live_out_ids`, `out_not_in`, `live_in_bits`,
`live_out_bits`) answer the same as before. A value without a slot reads as
not live, and the id lists stay ascending.

## Measured

The whole compiler under callgrind. Both compilers compiled the same frozen
copy of the tree (main with #12176's change), each on a warm memo of its own.

| | Ir |
|---|--:|
| before | 71.368 G |
| rows over the values that cross blocks | 70.706 G (−0.93%) |

`compute_checked` went from 1,546 M to 1,071 M, `bit_has` −129 M, `bit_set`
−68 M and `set_ids` −70 M. `add_local_use` is gone (−105 M), and recording the
exposed reads costs 151 M.

The changed compiler compiles `checker.fern` to the same bytes for x86-64,
arm64 and wasm, and `fern.fern` for x86-64 and arm64, and rebuilds itself byte
for byte.
