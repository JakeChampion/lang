# 2026-10-12 — a split round holds its defs once

`seminline.split_round`. Closes #11913: both sites it named copy nothing.

## The shape

`split_round` built its `Edits` from a local `defs` and kept the local alive
through the round, to ask `splittable(f, defs, ...)` of each read. So the
`Defs` box had two holders, the local and the edits, and the first part phi
the round made (`part_of`'s phi branch, `d0.insts.append` and
`d0.blocks.append`) found the box shared, retained both arrays and copied
them whole. Every later append in the round went to the fresh copies, so it
was one copy of each array per round that split a phi: 92 in all compiling
`checker.fern`, attributed by return address under gdb
(`docs/LOCAL-DEV-LOOP.md`).

`splittable` reads only values that were in the graph before the round, and
the edits only append past them or write the slots they appended, so it
reads `e.defs` with the same answer. The edits are then the only holder.

## Measured

The append cliff at stage 2 compiling `checker.fern` (`scripts/cliff-bench`
method, x86-64):

| | bytes | crossings |
|---|--:|--:|
| main (57a5849) | 22,999,176 | 193,788 |
| this change | 22,670,472 | 193,696 |

The 328,704 bytes and 92 crossings are `part_of`'s whole remainder.
`semsource.define` already copied nothing
(`2026-10-11-d-a-read-held-past-its-records-death-takes-its-unit.md`).
Against the issue's 75.0 MB at its opening the cliff is down 52.3 MB.

## What is left

The remaining holders are not the attempt-and-rollback shape:
`checker.Scope.bind` (6.6 MB, a flat scope's name and type rows appended
per binding while an enclosing scope still holds them),
`checker.inst_stmt`'s closure (3.9 MB), `lift.lift_lambdas_view_typed`
(3.9 MB), `util.append_all` under `seminline.splice_calls` (2.1 MB, a true
concatenation), `lexical.bind` (1.6 MB) and `ssasem.analyze` (1.1 MB).
