# 2026-10-06 — the block merge fills its matrix with a store

`ssa_lift.lift_impl`, the block-exit merge. Refs #8171. No emitted byte
changes: the compiler before and after builds `checker.fern` for x86-64,
arm64 and wasm, and `fern.fern` for x86-64, byte for byte.

## What changed

At a block's end the lift builds a matrix, one row per slot the block
wrote and one column per edge into the exit, seeded with each slot's
value at the open and then overlaid with what each edge recorded. The
seed wrote every cell through `put_at`, a call that tests the index
against the length and appends or stores: 3.2 M calls on a `checker.fern`
compile, one per cell, almost all of them stores into a matrix that is
kept across merges and already long enough. The matrix is now grown to
the merge's size once, by appends, and the seed is a `with` per cell.

A version that dropped the seed altogether, stamping each cell an edge
wrote with the merge's generation and settling a row from a count of its
writes, measured 230 M instructions WORSE: nearly every cell is written
by an edge anyway (an edge lists every slot written since the scope
opened), so the per-entry bookkeeping cost more than the seed it removed.
The matrix stays.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler from main at b6795704.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 17.208 G | 17.170 G (−0.22%) |
| `ssa_lift.put_at`, self | 100.8 M | 43.3 M |
| `ssa_lift.lift_impl`, self | 731.7 M | 750.2 M |

The 43 M that stay in `put_at` are the scope stacks, thirteen rows per
`if` or `block` opened.

## What is left in the lift

`lift_impl` is 4.4% of the compile and its cost is spread over the
per-row loops, by the per-instruction profile: recording an out-edge's
slot values at a `br` (90 M, two appends per row), the fold of a closing
scope's rows into its parent (about 140 M, a bounds-checked read and a
`with` per row), the merge's overlay and agreement scan (90 M) and the
op dispatch itself. None of them is a call per cell any more; the next
step down is a design that records edge values in the rows' own order so
the merge reads them without a matrix.
