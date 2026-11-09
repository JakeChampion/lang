# The word fixpoint goes by components

`seminline.returned_words_since` finds which functions return a word that they read from memory.
Between two splice rounds, it found the answer again for each stale function and for each of its transitive callers.
It reset the whole region to false and found the least fixpoint of the region again.

In one compile of the subject, no round after the first changed an answer.
But a round evaluated up to 2,323 functions, for 1,005 stale functions.

A function whose answer did not change cannot change a caller, except through a cycle of calls.
`returned_words_since` now splits the region into its strongly connected components, with `region_sccs`.
It finds the components again in order, callees first.
A component is found again only when it holds a stale function, or when it calls a component whose answer changed.
It resets the whole component, so a cycle gets the least fixpoint, as the old reset gave.

`TestSelfHostInlineOpaqueReturnSummary` has one more case, on x86-64, arm64 and wasm.
Two functions call each other, and one of them first reads its word from a third function.
Then that function calls only the other one, and the least fixpoint is false for both.
The case fails when each function is found again alone, without its component.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,476,438,410 |
| this change | 29,376,772,606 (−0.34%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
