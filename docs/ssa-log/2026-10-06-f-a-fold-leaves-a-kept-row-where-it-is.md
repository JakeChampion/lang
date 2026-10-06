# 2026-10-06 — a fold leaves a kept row where it is

`ssa_lift.lift_impl`, every target. Refs #8171.

## The shape

When a scope closes, the lift folds the slots it wrote into the enclosing
scope: a row whose slot the parent had not written yet is kept, and the
rest are dropped, since the parent already has a row for the slot. The kept
rows are packed down over the dropped ones, three `with`s per row. On
`checker.fern`, 2.90 M of the 2.96 M rows folded at `block` ends are kept,
so nearly every fold drops nothing and the three writes put each row back
where it already was, at some 40 instructions a row: a `with` on an owned
array is a tag test, a count test, a bounds check and the store, and the
array is reloaded from the frame for each.

The fold now writes a row only when it moves. The same loop closes an `if`
and a loop body, and both take the same test.

The `block` merge also built its edge matrix afresh at every merge, one
`append` per cell, 1.89 M cells over 36,964 merges. The matrix is one scratch
array now, kept across merges and grown to the largest, written with `with`
through `put_at`; the value a row starts from is read once per row rather
than once per cell.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at 2c1e37d6 against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,004 rows:

| | main | this branch |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 18.446 G | 18.354 G (−0.50%) |
| `ssa_lift.lift_impl`, self | 976 M | 835 M |
| `ssa_lift.lift_impl`, inclusive | 1,268 M | 1,177 M |

## What is left in `lift_impl`

The per-line profile after this change puts the largest remaining cost on
the parallel moves the register allocator places where the op loop's arms
join: each arm's edge permutes a dozen registers and copies several spilled
values frame to frame, some 13 to 37 moves an arm, about 130 M in all.
`2026-10-05-z` made a merge phi share the loop header's home where the two
never overlap by path; what remains here is a different pattern, where
merge phis take each other's registers in a cycle, and is the next lead.
