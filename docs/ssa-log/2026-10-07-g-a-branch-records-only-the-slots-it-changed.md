# 2026-10-07 — a branch records only the slots it changed

`ssa_lift.lift_impl`, the `br` / `brif` arm. Refs #8171. Follows
2026-10-06-m-the-block-merge-fills-its-matrix-with-a-store, whose "what is
left" named this recording as one of the lift's three per-row costs.

## Before

A `br` or `brif` records an out-edge for the scope it leaves to: the
predecessor block, and the value of every slot written since that scope
opened, as two appends per row. The scope's merge at `end` reads an edge's
unrecorded slot as its value at the open: a block exit's row default is the
row's `dl_old`, and a loop's back edge defaults to the header phi, which is
the slot's value once the loop opened. A row whose value on the edge is still
`dl_old` therefore told the merge nothing it would not have assumed. It was
recorded all the same, and the merge then wrote it over its own default.

On a compile of `checker.fern` the two appends were the costliest lines in
the lift: 64 M Ir of `lift_impl`'s 610 M self.

## Change

The edge records a row only when the slot's value differs from the row's
`dl_old`.

Every row in a scope's table holds the slot's value at that scope's open, and
a row an inner scope adds and later folds up survives the fold only when the
outer scope had not written the slot first, so its `dl_old` is the same value.
When an inner row and an outer row name the same slot at a branch, both carry
the slot's one current value, so whichever is recorded, the merge reads that
value; when neither is recorded, the value is the outer row's default. Every
merge reads the same operands as before, so each phi has the same arguments
and no phi appears or disappears.

## Measured

`checker.fern` to an x86-64 binary under callgrind. Each side's stage 3 is
built by its own stage 2, with no `-g`. Both stage 3s rebuild themselves byte
for byte.

| | main (cdf55f22c) | this change |
|---|--:|--:|
| total Ir | 14,538,423,008 | 14,495,524,365 (−0.30%) |

Byte-identical against a compiler built from main: `checker.fern` for
x86-64, arm64 and wasm32-wasi, and `fern.fern` for x86-64.

## Left

The scope fold, which walks a closing scope's rows into its parent (lines
2050–2066 and their unlined condition code, about 80 M), and the block merge's
matrix fill and agreement scan.
