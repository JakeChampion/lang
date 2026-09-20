# 2026-09-20 — a function value is a variant field

The verifier's `function value is not a field` was the census's largest
verifier root (72 sites after the two slices before it), all of it four
programs: the `std/async` combinator tests and the three `std/sim` tests.
The shape is `Future[T]`'s `Pending(i32, (i32) => Future[T])`: a function
value held DIRECTLY by a variant field. A record field has held one since
#9637 — the holder takes a unit of it and its release walks the captures —
but `ssasem.field_placement_error` still refused every enum field that
held a function value at any depth, nested or not.

## What changed

The enum loop takes the record's rule: a function value directly in a
variant field is admitted; nested in an array or tuple behind one it stays
refused. Nothing else was needed: `ssarc.drop_variant_fields` already
selects the box's variant and drops its fields as a record's, through
`drop_value` → `drop_children` → `drop_captures`, and
`semsource.env_rows_closed` already collects the environment rows of a
function type reachable through an enum field. The production row
`closure-in-a-variant-field` (a `Step` chain of `Next(i32, (i32) => Step)`
closures, run and dropped) is pinned `noLeak`: the typed lowering frees all
29 boxes the program builds; the AST lowering frees none of them, filed as
#9841.

## Measured

Corpus census, x86-64, `FERN_SEM_IR_REPORT=1`, 864 programs:

| | before | after |
|---|---|---|
| `function value is not a field` sites | 72 | 0 |
| declarations produced whole | 71,254 of 86,879 | 71,254 of 86,879 |
| programs produced whole | 765 | 765 |

The tally did not move, and that is the finding: every function the rule
held is held again one root further along. The async tests reach `poll`,
`wasm_timer_pollable` and `wasm_pollable_drop` through `RealDriver`, which
have no contract (the leftover OS floor, 72 sites: `tcp_send`,
`set_file_times`, `window_size`, `mknod`, `poll` and the pollables, a tail
of ones), and the test bodies fall under the mixed-module rule `calls a
function value of 0 arguments, a type the AST lowering builds a value of`
because the test runner's `it` is handed a function the AST-lowered
`main` builds. The shape is admitted and pinned; the programs follow when
those two close.

## Trap

A purity check that compiles the working tree with two compilers compares
two SOURCES when the tree changed between the builds. The first comparison
here differed in exactly one function, `field_placement_error`, whose source
this slice edited; recompiling the same tree with both compilers agreed byte
for byte. Compile one tree, once per compiler, in the same sitting.
