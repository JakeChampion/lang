# The word fixpoint keeps its call edges

`seminline.returned_words_since` finds the returned words again for the changed functions and their callers.
It needs the callers of each function.
`word_callers` found them for the whole program at each request, through each call in each body.
Between two rounds, only the spliced functions change their callees.

`Words` now holds the callees of each function and the callers that they give.
`linked_words` finds all of them at the first request.
After that, it changes the edges of each stale function only.
It removes the function from the callers of its old callees.
Then it adds the function to the callers of its new callees.

An unchanged function keeps its call edges, so the callers are the same as `word_callers` finds for the whole program.
The order of a callers list can change, but the fixpoint does not depend on that order.

`TestSelfHostInlineOpaqueReturnSummary` has one more case, on x86-64, arm64 and wasm.
A caller changes from a call to `a` to a call to `c`, and then the body of `c` changes.
The caller is not marked in the second step, but its answer changes with `c`.
The case fails when the new edges of a stale function are not added.

## Measurement

The subject is the fixed `compiler/checker.fern` and its imports from the commit before this change.
Each compiler was built by itself from its own tree for x86-64, without `-g`.
Each compile ran under Callgrind 3.22 with `--cache-sim=no --branch-sim=no` and a separate primed proof cache.

| compiler | Ir |
| --- | --: |
| before | 29,589,736,628 |
| this change | 29,485,600,649 (−0.35%) |

The two compilers emit a byte-identical x86-64 executable, and byte-identical arm64 and wasm assembly, for the subject.
