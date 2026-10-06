# 2026-10-06 — liveness rebuilds a row from the successors

`ssalive.compute_checked`, the live-in / live-out fixpoint every unit plan
and register allocation reads. Refs #8171. No emitted byte changes: the
compiler before and after builds `checker.fern` for x86-64, arm64 and
wasm, and `fern.fern` for x86-64, byte for byte.

## What changed

Recomputing a block began by copying its last live-out row word for
word, then OR-ing each successor's live-in over it. The successors' sets
only grow over the fixpoint, so their union holds everything the old row
held: the first successor now seeds the row directly and the copy is
gone; a block with no successor zeroes it.

The same recompute walked every instruction of each successor to find
its phis, once per predecessor per recompute. The phis' positions are
now listed per block once, in the pass that builds the use and def sets,
and the recompute reads only them.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at a0c4de43 by the stage0 pin.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.951 G | 16.904 G (−0.27%) |
| `ssalive.compute_checked` (inlined into `ssadeps.analyze`), self | 276.8 M | 228.8 M |

## What is left

The fixpoint is a dense bitset over every value of the function, `words`
words per block per pass: the per-word OR and the per-word live-in update
are now most of the cost, about 60 M and 50 M on this compile by the
per-instruction profile. The values a
block can see are numbered close together, so a per-block word range, or
a sparse row, would skip the zero words a large function's blocks mostly
hold.
