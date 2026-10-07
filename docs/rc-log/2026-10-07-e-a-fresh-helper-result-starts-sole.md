# 2026-10-07 — a fresh helper's result starts sole

`ssaunits.fresh_rows` and `semlower.rows_planned`. Refs #8171. Builds on the
previous two entries.

## What changed

The sole-box proof never seeded a call's result, because it does not see
into the callee. A table that a helper builds and returns, such as the
lift's `filled(nslots, 0 - 1)`, therefore kept the run-time uniqueness test
on every write, though the helper's own body already proves its result sole.

`fresh_rows` names the rows of a module whose every return hands back a sole
array. A row joins once its returns are sole with the rows already named as
seeds, so a helper that passes another helper's result on joins a round
later. Recursion never starts sole and adds nothing. A call to a named row
is then a seed like `__alloc_i32`.

The set is only known once every row of the module is planned, so
`rows_planned` builds it after planning and proves the receivers again
(`with_fresh`) for each row that calls a named row. A plan records whether
its receivers counted a fresh result (`fresh_seeded`), so a plan the
inference takes back from the declared pass is proved again under the new
set rather than kept. A row whose callees' freshness differs between the
declared and inferred sets is lowered under the inferred plans, as a moved
grow mask already is.

## Measured

`checker.fern` (at 2e084b79) built for x86-64-linux under callgrind by
production compilers, no `-g`, each side's stage 3 built as before. The
baseline is the previous entry's branch at 9f4ab5f1c. Both stage 3s rebuild
themselves byte for byte.

| | before | this change |
|---|--:|--:|
| total Ir | 15.544 G | 15.529 G (−0.09%) |
| stage 3 size | 10,932,872 | 10,929,320 |

On a `-g` stage 2 the inlined uniqueness test falls from 703 M to 650 M, and
`ssa_lift.lift_impl`'s share from 56 M to 14 M: the `slot_pos` and
`slot_mark` writes no longer ask.

## What is left

The set is per module. `util.minus_ones` is the same shape as `filled`, but
its callers are in other modules, which see no summary of it.

The largest remaining site is the liveness row in `ssadeps.analyze` (18 M).
The row starts sole, but `add_use` takes it `own` and hands it back, and a
call is not a use the proof keeps a box sole through. `ssa_lift.put_at` (8 M)
is the same shape. A second summary, that a row's result is sole whenever
its owned array parameter arrives sole, would let such a call act like an
`append`.

The release paths' count tests (the `__sem_drop_*` functions, about 45 M)
stay.

The test is `TestSelfHostSoleLoopWithX86_64`. Two shapes join the proved
ones (a helper's result written in a loop, and the same through a second
helper that passes it on), and two join the refused ones (a helper that
returns the array it was given, and a helper that returns a constant
literal).
