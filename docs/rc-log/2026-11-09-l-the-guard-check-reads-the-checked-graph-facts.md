# The guard check reads the checked graph facts

`ssasem.analyze` checks the graph of a function first, with `ssadeps.check_graph`.
The facts that it keeps hold the dominator tree, the block positions and the reachable blocks.
Then `operations_error` checks that each variant projection has a guard.
That check built the dominator tree, the block positions and the reachable blocks again for the same graph.

`operations_error` and `guard_error` now take the facts.
They build the three maps only when the verifier is off, because then the facts are unchecked.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,539,973,653 |
| this change | 29,497,983,514 (−0.14%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
