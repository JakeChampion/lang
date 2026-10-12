# 2026-10-12 — a dead-store round follows an emptied slot

`ir.eliminate_dead_stores`. Refs #8171. No emitted byte changes.

## The shape

`eliminate_dead_stores` turns each store to a slot nothing loads into a drop,
then cancels each drop against the constant or load that pushed what it drops.
A cancelled load can leave its slot unread, so the pass repeated. It stopped
when a round left the op list the same length. Every round started by counting
each slot's reads across the whole list again (`slot_reads`, and
`cp_max_slot` to size the counts). So a function with any dead store paid a
whole extra round only to find nothing more to do. That was 5,177 of 7,664
calls on the whole compiler's self-compile.

## What changed

- The read counts are taken once and carried from round to round.
  `cancel_dropped_pushes` takes them, lowers a slot's count for each load it
  cancels, and says whether one reached zero (`Cancelled`).
- Another round runs only when a count reached zero. Nothing else can give a
  round new work:
  - `drop_dead_stores` has already dropped every write to a slot with no
    reads;
  - the cancel pass unwinds as a stack, so one pass leaves nothing more to
    cancel.

`ds_copies` and `ds_read_again` in `ir_strength_run.fern` pin the two shapes
the counts must get right. One is a copy chain that needs a round per copy;
the other is a cancelled load that is not its slot's last read.

## Measured

The whole compiler under callgrind. Both compilers were built from the same
tree and compiled the same frozen copy of it, each on a warm memo of its own.

| | Ir |
|---|--:|
| before | 69.983 G |
| reads counted once | 69.743 G (−0.34%) |

All of it is in `ir` (−226 M): `ir.cleanup`, where the pass is inlined, went
from 775 M of its own to 599 M, and `cp_max_slot` lost 51 M of its calls.

The changed compiler compiles `checker.fern` and `fern.fern` to the same bytes
for x86-64 and arm64, and `checker.fern` for wasm. It also rebuilds itself
byte for byte.
