# ssa: a structural fingerprint for the optimiser's fixpoint

**Date:** 2026-09-16

## What the profile said

`ssa.Optimize` runs its pass list until an iteration changes nothing, and it
told by rendering the function to text before and after each iteration and
comparing the strings. On the x86-64 SSA build of
`examples/self_host/asm_ir_run.fern` (41 s wall) `(*Func).String` from
`Optimize` was 2.6 s, more than SCCP, the most expensive pass it was checking.

## Change

`fingerprint` hashes what the passes can change: the block list, each op's
kind, results, operands, immediates, string, width and address flag, capture
slots, the predecessor lists and the terminators. FNV-1a over 64-bit words,
no allocation. `Optimize` compares fingerprints; `runPasses` is one iteration
of the pipeline, split out so a test can run the old text comparison against
it.

The fingerprint covers strictly more than the text did (the rendering omits
width and address), so a change the text would have caught, it catches. A hash
collision would end the loop an iteration early, which costs an optimisation,
never correctness, and every pass is idempotent on a settled function.

## Result

Driver compile, alternating runs on the same machine, from a base that does
not yet carry the single-emission change (#9463):

| Build | Compile |
|---|---|
| main | 40.3 s, 43.1 s |
| this branch | 37.7 s, 38.7 s |

The emitted assembly is byte-identical to main's: the loop stops after the
same iterations, so every function is optimised exactly as before.

## Tests

- `TestFingerprintTracksEveryField`: an edit to each field, block order, a
  predecessor list or a terminator moves the fingerprint; identical functions
  agree.
- `TestOptimizeConvergesWhereTextComparisonDid`: over a corpus that takes
  two to four iterations to settle, `Optimize` takes the same number of
  iterations as the text comparison and produces the same function.
