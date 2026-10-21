# 2026-10-21 — a call finds its leaf by one lookup

Both native backends. `ssa.Leaves`, `ssa.leaves_of`, `ssa.leaf_index`,
`ssa.inline_leaves`, `ssa.calls_leaf`; `asmcore.unit_leaves`,
`EmitState.leaves`; `ssaprepare.finish`. Refs #8171.

## The shape

A unit's leaves are the small bodies a caller may take inline.
`inline_leaves` asked `leaf_index` about every call instruction of every
function, and `calls_leaf` the same once more to decide which register
forms to keep; `leaf_index` scanned the leaf list comparing names. Calls
times leaves per unit: 198 M instructions on the whole compiler's
self-compile, thirteen times the `checker.fern` figure for a subject five
times smaller.

## What changed

A unit's leaves travel as `ssa.Leaves`, the list with its names indexed
once by `leaves_of`, and `leaf_index` walks the name's chain checking the
arity. The chain ascends, so the first hit is the first leaf of the list,
as the scan answered.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the constant-pool slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 76.707 G | 76.526 G (−0.24%) |

The whole-compiler binary is byte-identical to the base compiler's.
