# 2026-10-04 — the carry routes mark instead of scanning

`ssarc`, both targets. Refs #8171.

## The shape

`ssarc.carried_pairs` (#11480) looks for a route from every block of a
function to the first construction its dropped box can reach. That costs
two searches per block:

- **`carry_route`** walks forward from the block over the blocks that build
  nothing.
- **`route_to`** walks back from each candidate construction.

Both searches did their bookkeeping by scanning:

- each block id went through `ssarc.block_index`, a linear scan of the
  function's blocks;
- each "seen yet?" test was `util.index_of_i32` over the lists the search was
  growing;
- `first_claim`, which scans a block's instructions, ran again every time a
  search reached that block.

On a large function this is quadratic per block, so cubic in total.
Compiling `checker.fern`, `block_index` took 1.63 G Ir and `index_of_i32`
1.41 G. That made them the two most expensive functions in the stage-2
compile, ahead of `ssa_lift.lift_impl`.

`carried_pairs` now computes three things once per function, in
`route_graph`:

- the block positions by id, from `ssa.block_positions`;
- whether each block builds;
- an all-zero mark array.

`carry_route` copies the mark array and records in it which blocks are ahead
and which build. `route_to` copies the caller's marks and records the
blocks between in its copy, so a target that fails leaves the caller's marks
as they were. The lists the searches build stay, so every walk visits
blocks in the same order and the routes come out the same.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at ddf8b959 against this branch. Both compilers build the
same `checker.fern` to a byte-identical binary for x86-64-linux and
arm64-linux, and build `fern.fern` identically too:

| | main | marks |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 23.517 G | 20.397 G (−13.3%) |
| `ssarc.carried_pairs`, inclusive | 3,430 M | 311 M |
| `ssarc.block_index`, self | 1,631 M | 13 M |
| `util.index_of_i32`, self | 1,405 M | 4 M |
| `ssarc.first_claim`, inclusive | 147 M | 10 M |

Before #11480 this compile measured about 20.3 G on x86-64, so the change
takes back almost all of that cost. The arm64-linux compile carries the
same 3 G, since `ssarc` does not depend on the target.

## What is left

`carry_route` and `route_to` still cost 288 M between them. Most of it is the
walks themselves, the successor lists they rebuild, and about 33 M copying
the mark arrays: each block copies one per search, so a function of N
blocks copies N arrays of N entries. Stamping one shared array per function
would remove those copies.
