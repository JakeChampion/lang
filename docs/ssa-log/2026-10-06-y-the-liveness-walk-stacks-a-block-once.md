# 2026-10-06 — the liveness walk stacks a block once

`ssa.live_at_def`, the register allocator's question "is r live where a
is defined", asked 15 k times on a `checker.fern` compile and answered by
a walk of the blocks reachable from a's. Refs #8171. No emitted byte
changes: the compiler before and after builds `checker.fern` for x86-64,
arm64 and wasm, and `fern.fern` for x86-64, byte for byte.

## What changed

The walk kept its pending blocks in a list it grew by one append per
edge, marked a block only when it popped it, so an edge to a block already
pending appended it again, and read each block's successors from a nested
list the sites table held per block, a retain and a release per visit. It
also took its visited set as a copy of an all-false row the table carried.

The walk now marks a block when it first reaches it and stacks it once,
in one allocation per walk that holds the stack and the marks together;
the sites table carries two flat successor columns instead of the nested
list, read by index, and the all-false row is gone.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 2e084b79 by the stage0 pin;
the baseline is main at b5a688d3.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.863 G | 16.823 G (−0.24%) |
| `ssa.live_at_def`, self | 135.3 M | 109.7 M |
| `ssa.read_sites`, self | 54.2 M | 56.6 M |

## A measurement trap

`profile.sh` builds the stage-2 compiler with `-g` so the symbols resolve.
`-g` adds a position marker per statement to the SSA, which the semantic
inliner counts toward its 1500-instruction caller cap, so a `-g` build
splices less than a production one: the `-g` stage 2 keeps 165 calls to
`checker.t_unknown` that the production build has spliced away. Totals
measured on `-g` builds compare with each other, not with production
numbers (a production stage 3 compiles `checker.fern` in 16.688 G against
the `-g` build's 16.863 G), and a change to the inliner's policy has to be
measured on a production build. A production binary carries no symbol
table, so it gives totals only.

Since 2026-10-07 the semantic inliner neither counts the markers nor lets
one keep a function from being a leaf (`seminline.line_mark`), so a `-g`
build splices what a production one does.

## What is left

Each walk still allocates its stack and marks and still visits, on
average, most of the function: a `false` answer is reached only by
exhausting what a's block reaches. The dominators would answer most of
those at once (r's block not dominating a's means every path to a read
passes r's definition), but they live in `ssadeps`, which imports this
module.
