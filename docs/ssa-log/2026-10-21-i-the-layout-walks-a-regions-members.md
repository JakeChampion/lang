# 2026-10-21 — the layout walks a region's members

`ssalayout`: `absorb`, `next_ready`, and the region helpers. Refs #8171.

## The shape

`ssalayout.compute` orders a function's blocks for the native backends,
once per function from `ssarc.lower`. In the per-instruction profile of
the whole-compiler self-compile it was 515 M Ir of self cost, in three
shapes that scale with the block count n rather than with the region:

- `absorb` copied its whole work stack to pop one entry, a copy per block
  of every natural loop;
- `next_ready` scanned every position from zero for each block it placed;
- `representatives`, `outermost`, `members_by_rep`, `pending_edges`,
  `complete` and the fallback each scanned all n positions and tested
  membership, once per region, so every nested loop cost a whole-function
  scan per helper.

## What changed

The work stack keeps its storage and a depth. A region carries its members
in ascending position as well as the mask, and the helpers walk the list;
the fallback keeps a floor into it below which nothing is ready, moved back
only when a release brings a lower representative to zero, since a
representative stays ready from that moment until it is placed. The order
placed is the same: the fallback still answers the lowest ready
representative, and the list is ascending.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | main at 42d35e4 | stack and floor | region lists |
|---|--:|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.406 G | 74.332 G (−0.10%) | 74.257 G (−0.20%) |

The whole-compiler binary is byte-identical to the base compiler's after
each commit. What remains in the module is linear per region: the n-sized
rows and masks built per nested loop, and the n-by-n member matrix the
loops are found into.
