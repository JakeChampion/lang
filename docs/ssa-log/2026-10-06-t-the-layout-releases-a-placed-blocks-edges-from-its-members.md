# 2026-10-06 — the layout releases a placed block's edges from its members

`ssalayout.region_order`, the block order the register lowering emits a
function in. Refs #8171. No emitted byte changes: the compiler before and
after builds `checker.fern` for x86-64, arm64 and wasm, and `fern.fern` for
x86-64, byte for byte.

## What changed

Placing a block in a region releases the pending edges out of its loop:
the blocks the loop's representative stands for, each successor of each,
one count off the successor's representative. `release` found the members
by scanning every block of the function and testing its representative,
once per placed block, so a region of `n` blocks cost `n` scans of `n`.

The members are now listed by representative once per region, in the
counting-sort shape the allocator's interval order uses, and a release
walks only the placed representative's list. The representative table
itself is a filled allocation instead of an append per block.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 2e084b79 by the stage0 pin.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.862 G | 16.786 G (−0.45%) |
| `ssalayout.region_order` (its helpers inline into it), self, both recursion levels | 146.4 M | 70.4 M |

## What is left

`next_ready` still falls back to a scan of every block when no successor
of the last placed block is ready, `complete` scans every block once per
region, and `representatives` scans every block once per loop header to
find whether it is outermost. A ready list kept by `release` would take
the first two; the loop nesting the loop pass already knows would take the
third.
