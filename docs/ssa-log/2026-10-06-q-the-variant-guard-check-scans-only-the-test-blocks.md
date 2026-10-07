# 2026-10-06 — the variant-guard check scans only the test blocks

`ssasem.guard_error`, the part of the typed analysis that proves every
variant and dyn projection sits where a test has settled the value's
shape. Refs #8171. No emitted byte changes: the compiler before and after
builds `checker.fern` for x86-64, arm64 and wasm, and `fern.fern` for
x86-64, byte for byte.

## What changed

The check ran on every function: dominators, reachability and a
definition table per value, built whether or not the function projected
anything. For each projection it then walked every block of the function
looking for the branches that test the projected value, and for each
such branch found the branch's two targets by a linear search of the
block list for their ids, twice per test.

It now does nothing in a function with no variant or dyn projection. In
a function with one it builds the dominators, the definition table and
the block-position table once, lists the positions of the blocks whose
two-way branch tests a value's shape once, and a projection's check
walks that list, reading each target's position from the table.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 2e084b79 by the stage0 pin.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.862 G | 16.763 G (−0.59%) |
| `ssasem.sole_successor`, self | 33.0 M | 1.9 M |
| `ssadeps.dominates`, self | 64.6 M | 45.6 M |
| `ssasem.operations_error` (the walk inlines into it), self | 43.5 M | 31.1 M |
| `ssasem.definitions`, self | 50.2 M | 41.4 M |

The dominator and definition-table drops are the functions that project
nothing; the rest of their cost is the functions that do.

## What is left

Dominators are computed afresh by each pass that wants them on the same
graph: the guard check and the view-merge scan in `ssasem`, the bounds
pass, the layout pass. `definitions` copies every instruction into a
table per value, 41 M a compile across its eight callers, where a table
of positions would do for most of them.
