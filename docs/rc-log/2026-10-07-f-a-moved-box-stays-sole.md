# 2026-10-07 — a moved box stays sole, and a helper can pass one through

`ssaunits.sole_boxes` and `ssaunits.fresh_rows`. Refs #8171. Builds on the
previous three entries.

## What changed

Two rules, one general and one a summary.

**A use that moves a box is its last.** The proof cleared a box at any use
other than reading it, being a `with`'s or `append`'s receiver, or flowing
into a phi. That was stricter than it needs to be. A step moves a value only
when the value is dead after it (`choose_ids`), so whatever the use does
with the box, the frame never reads it again, and no holder the use adds can
see a write the frame makes. A box handed to a call that takes it owned, or
stored into a record as its last use, now stays sole up to that point. A use
that lends or retains still ends it, as before: a callee lent a box, or a
record built while the frame still reads it, can hold it while the frame
writes.

**A helper that hands back its owned array passes soleness through.**
`FreshRows` now records, beside the rows whose result is always sole, the
rows whose result is sole whenever one owned array parameter arrives sole:
`ssa_lift.put_at`, which writes or grows the array it was given, and
`ssalive.add_use`, which returns its row unchanged on one path. A call to
such a row is treated as an `append` is: its result is sole when the
argument at that parameter is sole and moved into it. The summary proves
the parameter case by running the same analysis with the parameter assumed
sole, and only for a parameter the row takes in the counted mode, the one a
caller moves into.

## Measured

`checker.fern` (at 2e084b79) built for x86-64-linux under callgrind by
production compilers, no `-g`, each side's stage 3 built as before. The
baseline is main at f009e7a36 with the fix for #11799. Both stage 3s
rebuild themselves byte for byte.

| | before | this change |
|---|--:|--:|
| total Ir | 15.433 G | 15.292 G (−0.91%) |
| stage 3 size | 10,954,584 | 10,768,496 (−1.7%) |

## What is left

On a `-g` stage 2 the inlined uniqueness test costs 518 M on the same
compile, and the liveness row's writes in `ssadeps.analyze` (18 M) no
longer ask.

Most of the rest is inside the helpers themselves. `ssa_lift.put_at` (8 M),
`x86_native.x86_put` (10 M) and `ssalive.bit_set` (5 M) each test the owned
parameter they write, because their body cannot know what every caller
hands it. The proof now knows that the callers in these loops hand over a
sole box, so a copy of the helper for sole arguments, or splicing it into
the caller, would let those tests go too.

`ssarc.emit` appends to a record's field, `r.ops`, which the field-grow path
tests at run time on both the record and the buffer (about 15 M). The
release paths' count tests (the `__sem_drop_*` functions, about 45 M) stay.

The test is `TestSelfHostSoleLoopWithX86_64`. Three shapes join the proved
ones: a table threaded through a `put_at`-shaped helper and then written, a
table passed to a helper that always writes it, and a loop's array stored
into a record by its last use. The first and the last fail without this
change.
