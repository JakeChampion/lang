# 2026-10-11 — a split round reads its edits once

`seminline.part_of`, `seminline.part_value`. Refs #11913, the `part_of`
half; its 1.5 MB after #12134 is now 0.3 MB. The append cliff compiling
`checker.fern` (`scripts/cliff-bench`, x86-64, stage 2 on the dev container):
24,148,960 bytes over 193,521 crossings to 22,945,424 over 192,943.

## The shape

```fern
Edits { ...w.e, defs: Defs { insts: w.e.defs.insts.append(x), blocks: w.e.defs.blocks.append(pb) }, added: w.e.added.append(ty) }
```

`w` is owned, and so are its edits and their definitions, yet every append in
the construction copied its array. The construction reads `w.e` four times
and `w.e.defs` twice, each read its own instruction. A record whose slot is
read again is not taken apart at the earlier read (`needed_past`), so each
read borrowed, the array came out of a borrowed record retained, and the
append found it shared. The probe in #12135 pins the two shapes: the
construction above copies three arrays, and the same construction over
`let e0 = w.e; let d0 = e0.defs;` copies none.

The compiler cannot merge the reads itself: the rewrite that does so in
`semsource.define` lets the plan take a record apart in a way the compiler's
own build does not survive (#12135). Until that is understood, a
construction over an owned record reads each record on the way to an array
once. `part_of` and `part_value` now do, and `next_value` goes with the
reads it wrapped.

## Measured

| | bytes | crossings |
|---|--:|--:|
| main (b3766ab) | 24,148,960 | 193,521 |
| this change | 22,945,424 | 192,943 |

`part_of` is left with 0.3 MB: 311 appends onto empty arrays, which copy
nothing, and the function's own definitions where a round's first part phi
is reached from a block the definitions were read in. `define` copies
nothing. The two sites the issue named are closed by their own measure, and
against the 75.0 MB the issue started from the cliff is down 52.1 MB on the
dev container; the lane's reading of main decides the issue.
