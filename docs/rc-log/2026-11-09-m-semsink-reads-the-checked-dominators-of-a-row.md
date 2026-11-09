# semsink reads the checked dominators of a row

`semsource.with_lent_rereads` re-reads projections in each row after ownership inference.
Ownership inference changes only the call contracts of a row, so the analysis of the row still describes its graph.
`semsink.reads_of` and `semlatehold.graph_of` built the dominator tree of that graph again.
`reads_of` also built the block positions again.

`semsink.sink_past_lent` now takes the graph facts of the analysis.
`reads_of` and `semlatehold.graph_of` read the dominator tree and the block positions from the facts when the facts are checked.
`semlatehold` gets the facts only when `semsink` did not change the graph.
The other callers of `reads_of` give unchecked facts, and then it builds both maps as before.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,497,898,835 |
| this change | 29,443,649,178 (−0.18%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
