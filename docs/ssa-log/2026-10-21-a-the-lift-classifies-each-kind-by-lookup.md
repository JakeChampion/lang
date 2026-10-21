# 2026-10-21 — the lift classifies each kind by lookup

Both native backends. `ssa_lift.kind_table`, `kind_facts`, `listed`,
`admits`. Refs #8171.

## The shape

`kind_table` gives the lift one row per op kind id, 1024 of them, with the
class bits `lift_impl` branches on, the helper call's operand count and the
host-floor mark. It worked each row out by name: `kind_class` asked
`prod_kind`, `prod_mem_kind` and `prod_float_kind` of the name, and each of
those scanned its own list of names with a string compare per entry, then
asked `prod_call_argc` and `prod_host_kind`, which scanned theirs. About 150
compares per id, 1024 ids per table, and the table is built 16 times over a
stage-2 compile of `checker.fern`: once per module by the emit and the
certifier's gate, and once per retained runtime body the credit machines
check. The lift itself read the table per op, as it should; the cost was in
building it.

## What changed

The name lists stay, as the one statement of which kind is what, but they
are read the other way round. `kind_facts` indexes every listed name once in
a `util.NameIndex` with the facts each list grants it: a class bit for the
spine, the memory ops and the f64 unaries, the operand count, the host mark.
`kind_table` then finds each id's name in that index, one hash lookup, and
derives the row's bits from the facts and the two symbol tables (`bin_sym`,
`fbin_sym`) exactly as the per-name predicates did. The six predicates are
gone; `admits`, the admission census's question, reads the table's bit.

## Measured

Stage-2 compile of `checker.fern` under callgrind, warm certifier memo, both
compilers built by themselves from their own tree, compiling one frozen copy
of main at 2210347:

| | Ir |
|---|--:|
| main | 16.189 G |
| this | 16.114 G (−0.46%) |

The `checker.fern` binary is byte-identical, and so is every row of the
`selfhost-emit-hashes` sweep over the conformance corpus on all three
targets.

## Witnessed

`TestSelfHostSSALiftAdmitsEveryOp` pins the census the table answers, kind
by kind, against its golden; `TestSelfHostSSAKindRegistry`,
`TestSelfHostSSALiftGivesEachBlockOneID`, `TestSelfHostSSALiftDropsDeadRows`
and `TestSelfHostSSALiftPtrWidthIsAConstant` lift through the table.
