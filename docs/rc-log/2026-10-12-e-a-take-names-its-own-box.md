# 2026-10-12 — a take names its own box

Refs #11910. Three small cuts left by the use-site slice.

## The shape

`needed_past_slot` took the box as a parameter beside the read `take`, and
every caller passed the box the read comes from. Its store count reads that box
at operand slot 0 of a matching `with`, which is exact only while the two agree.
The box is now `take.args[0]`, read inside the function, so they cannot drift.

`dying_lent_rows` allocated a scratch set the size of the function for every
dying array operand at a call with a grow row. It now shares `grow_rows`' set,
which `carried_params` hands back clear.

`ssarc.derives` had no caller and is deleted.

## Measured

| | main | this |
|---|---|---|
| the whole compiler, compiled from main's sources | identical x86-64 assembly | identical |
| allocations compiling `checker.fern` | 23,691,746 | 23,689,974 |
