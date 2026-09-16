# x86_64ssa: emit each function body once

**Date:** 2026-09-16

## What the profile said

After the width worklist, bitset liveness, and the allocator and emitter size
work, a CPU profile of `fern -target x86-64-linux -backend ssa` on
`examples/self_host/asm_ir_run.fern` (41 s wall) put two emitter items near the
top:

| Item | Cumulative |
|---|---|
| `x86_64ssa.emitFuncBlocks` | 4.88 s |
| `regexp.(*Regexp).FindAllString` from `calleeSavedIn` | 3.95 s |

`emitFuncBody` emitted every function twice: once into a scratch buffer with no
callee-saved restores, so `calleeSavedIn` could scan the text for the registers
the function names, and then again for real with the matching pushes and pops.
The scan tokenised the whole probe text with a regexp.

## Change

The body is emitted once. `emitFuncBlocks` writes a marker line where each
return's pops go, `calleeSavedIn` scans that buffer, and the buffer is copied
out behind the prologue with the marker replaced by the pops. The pops name
only registers already in the set, so the set the scan found is the final one,
which is the same argument the two-pass version relied on.

`calleeSavedIn` tokenises by hand: the same identifier shape as the regexp
(`_` and `.` are word characters so a label cannot decompose into a register
name), a length cut at the longest register spelling, and a map from each
callee-saved register's four width spellings to its index. The regexp is gone.

## Result

Driver compile, same machine, alternating runs:

| Build | Compile |
|---|---|
| main | 43.0 s, 40.2 s |
| this branch | 36.0 s, 34.0 s |

The emitted assembly matches main's except for two `mov rcx, rcx` that main
kept: the variable-shift sequence is written as one multi-line string, so the
writer's dead-self-move filter, which looks at one call at a time, never saw
its inner line. The body is now copied to the writer a line at a time, so the
filter sees every line, whichever helper produced it.

## Tests

- `TestCalleeSavedInTokenisesRegisterNames`: a label carrying a register name,
  a helper symbol, a `.L` label and a digit-prefixed word are not uses; every
  width spelling is; the register file bound still applies.
- `TestRestoreMarkerIsReplacedByPops`: the marker never reaches the output, and
  a function whose parameter lives across a call pops what its prologue pushed.
- `TestWriteBodyReplacesMarkersLineByLine`: the copy replaces each marker with
  the pops in reverse push order, keeps blank lines, and drops a dead
  self-move.
- `TestCalleeSavedCoverage` (existing) still checks the finished text from
  outside the emitter.
