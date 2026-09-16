# arm64ssa: emit each function body once

**Date:** 2026-09-16

## Why

#9463 collapsed the x86-64 SSA emitter's two-pass function emission into one
and replaced its regexp register scan with a hand tokeniser, worth 6 s of a
41 s self-host driver compile. The arm64 backend carried the same two
mechanisms: `emitFunc` emitted each body once into a scratch buffer to
discover the callee-saved registers, then emitted it again for real, and
`calleeSavedIn` tokenised the probe text with `armRegTokenRe`.

## Change

The blocks are emitted once, into a buffer with a marker line where each
return's teardown goes. Nothing a block addresses moves with the saved set:
the frame puts spill slots, the call-save area and the out-argument area below
`csBase`, so their offsets are fixed before the scan. Only the prologue and the
teardown move with it, and they are rendered after the scan.

The parameter moves are scanned as well. A parameter's home can be a
callee-saved register that no block mentions, and dropping those lines from the
scan would return that register to the caller clobbered. They are rendered
twice — once against a provisional frame for the scan, once against the final
one for the output — because the layout reaches `paramMoveLines` only as slot
and incoming-argument offsets, so the two renders differ in immediates and
never in a register name.

`calleeSavedIn` now tokenises by hand: the identifier shape the regexp had
(`_` and `.` are word characters, so `.Lssa_x19_ok` stays one word), a length
cut at the longest register spelling, and a map from both width spellings to
the abstract index. The regexp is gone.

## Result

Driver compile for `-target arm64-linux -backend ssa`, alternating runs on the
same machine:

| Build | Compile |
|---|---|
| main | 39.9 s, 40.0 s |
| this branch | 34.4 s, 34.8 s |

The emitted assembly is byte-identical to main's, all 65 MB of it, so the
frames, the saved sets and the teardowns are exactly what the two-pass emitter
produced.

## Tests

- `TestCalleeSavedInTokenisesRegisterNames`: a label carrying a register name,
  a helper symbol and a digit-prefixed word are not uses; both width spellings
  are; the register-file bound still applies; the teardown marker is not a
  register mention.
- `TestParamMoveHomesReachTheScan`: a parameter homed in a callee-saved
  register is found in the parameter moves alone.
- `TestParamMoveRegistersDoNotMoveWithTheFrame`: the provisional and final
  renders name the same registers, which is what lets the scan run on the
  provisional one.
- `TestWriteBodyReplacesMarkersWithTeardown`: every marker becomes the
  teardown, and blank lines survive.
- The existing callee-saved suite (`TestCallCrossingValueUsesCalleeSavedRegister`,
  `TestLeafFunctionTouchesNoCalleeSavedRegisters`, `TestProloguePairsItsCalleeSavedSlots`,
  `TestRuntimeHelpersPreserveCalleeSaved`) checks the finished text.
