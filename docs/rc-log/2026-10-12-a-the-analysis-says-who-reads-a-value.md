# 2026-10-12 — the analysis says who reads a value

`ssasem.Uses`, `uses_only`, `use_count`. Refs #11910, the second slice: the
rules that asked what every use of a value is.

## The shape

Three questions about a value's uses scanned the whole function each time
they were asked. `ssaunits.sole_box` (may a payload read take a view or an
Option payload with no runtime test) asked whether every use of a box is a
tag test, a payload read or a phi; `only_element_reads` (may the untested
tuple take null a slot) asked whether every use is an element read; and the
reuse tables' `use_counts` counted the operands naming each value to find a
kept field the construction alone reads.

`ssasem.Analysis` now carries `uses`: for each value, the kind of every
instruction naming it among its operands, a return or a branch counted as
`term_use()`, in one flat table built with the dependencies (`uses_of`). The
three are queries over it: `uses_only(uses, v, kinds)` for the first two,
`use_count(uses, v)` for the third. `sole_box` keeps only its walk through
phis; `only_element_reads` and `use_counts` are deleted.

## Measured

`checker.fern`, `semsource.fern` and `ssaunits.fern` compile to
byte-identical x86-64 assembly with the compiler before and after.
