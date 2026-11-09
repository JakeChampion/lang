# The join split finds dominators only for a candidate

`semthread.split_constant_joins` splits a join whose branch value is a constant on each edge.
Each `split_round` found the dominators of the whole function before it looked at any join.
Most rounds find no join of the right shape, so most of those trees were not used.
`forward_empty_joins` found the dominators again in each of its rounds.

## What changes

`constant_join` now tests only the shape of a join.
`dominance_allows` holds the two tests that need dominators: the join dominates none of its predecessors, and `reads_under_arms`.
`split_round` finds the dominators at the first join of the right shape, and uses them for the joins after it.
The tests are a conjunction, so their order does not change which join is split.

`forward_block` keeps each block at its position, and it removes only the forwarded block from the paths.
So dominance between the other blocks does not change, and the forwarded block has no predecessors after it.
`forward_empty_joins` now keeps one dominator tree for all of its rounds.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from 65bc9710.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| 65bc9710 | 30,022,372,047 |
| this change | 29,867,940,391 (−0.51%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
