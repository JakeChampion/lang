# The inliner finds returned words again only where they can change

`seminline.returned_words` finds the functions whose result can be a word read from memory.
It is a least fixpoint over the call graph.
A function's answer depends only on its own body and on the answers of its callees.
The splice rounds asked it again for every function before each round.
The tiny rounds asked it twice for each round, once in `grown_tiny` and once in `splice_round`.
Between two rounds, only the functions that a round spliced into change.

## What changes

A `Words` value now goes through the splice rounds.
It holds the answers and marks the functions that changed after the answers were found.
`current_words` returns the answers unchanged when no function is marked.

Otherwise `returned_words_since` finds the answers again for the marked functions.
It does the same for each function that reaches a marked function through its calls.
Each other function keeps its answer, because no path through its callees reaches a changed function.
`returned_words` is `returned_words_since` with every function marked.

`TestSelfHostInlineOpaqueReturnSummary` now checks the new path on x86-64, arm64 and wasm.
A caller returns the result of a call to `a`.
When the body of `a` changes, the caller is not marked, but its answer changes with `a`.
The test fails when the callers are not found again.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from main at 65058acf.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| main at 65058acf | 30,650,646,392 |
| this change | 29,934,321,288 (−2.34%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
`scripts/selfhost-emit-hashes` gives the same hash for each of its 5,025 rows with the two compilers.
The `-g` builds of the two compilers attribute the difference as follows:

| function | calls before | Ir before | calls after | Ir after |
| --- | --: | --: | --: | --: |
| `seminline.inline_leaves` | 9 | 2,783 M | 9 | 2,067 M |
| the word fixpoint | 57 | 1,101 M | 27 | 385 M |
| `seminline.read_word_values` | 77,141 | 912 M | 18,738 | 274 M |

The other 30 requests found no changed function and did no work.

## What the profile shows next

`semsource.produce_module` is 25% of the compile, and the inliner is the largest part of it.
`semsource.build` is 1.50 G over 3,529 functions.
`semsource.prune_dead_constructions` is 1.08 G, and `semthread.thread` is 532 M of that.
`ssasem.analyze` under `semsource.anchor_module` is 982 M.
`semreach.reach` is 893 M over two calls.

In the inliner, `paired_all` is 273 M and `split_constructions` is 267 M.
`memory_read` costs about 205 instructions for each instruction that it tests.
It tests the callee name of a call with several string compares.
