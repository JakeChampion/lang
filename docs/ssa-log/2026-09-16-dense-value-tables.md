# ssa: dense tables where the passes keyed on Value.ID

**Date:** 2026-09-16

## What the profile said

A fresh profile of `fern -target x86-64-linux -backend ssa` on
`examples/self_host/asm_ir_run.fern`, after the emitter and fixpoint work of
#9463 and #9464 brought it to 32 s, put `runtime.mapassign_fast32` at 5.45 s
cumulative, 11% of the compile. The callers were the SSA passes, all writing
maps keyed by `Value.ID`:

| Caller | Assign time |
|---|---|
| `collectUses` | 1.08 s |
| `BuildUses` | 0.34 s |
| `Fold` | 0.33 s |
| `StrengthReduce` | 0.30 s |
| `Simplify` | 0.28 s |
| `Canonicalize` | 0.26 s |
| `SCCP`, `FoldBranches`, `TrivialPhis` | 0.24 s each |
| `CmpFlip` | 0.17 s |

Value IDs come from one counter per function, so they are dense. A map paid a
hash and a probe per access, and an allocation per growth step, to index what a
slice indexes directly.

## Change

`idTable[T]` is a slice of T indexed by `Value.ID`, sized from the function it
was built for. `get` answers the zero value for an invalid value or one minted
after the table was built, which is how the map behaved; `set` drops such a
value for the same reason.

The tables that now use it:

- `Uses.of`, the def-to-use index (`BuildUses`), whose public `Of`, `Count` and
  `HasUses` are unchanged.
- `collectUses`, the per-value use counts DCE and `ThreadPhiBranches` read.
- The `booleans` set in `ThreadPhiBranches`.
- The `defs` table that `Fold`, `Simplify`, `Canonicalize`, `CmpFlip`,
  `FoldBranches`, `StrengthReduce` and `SCCP` each build over every result in
  the function. The lookup helpers (`constInt`, `constBool`, `constFloat`,
  `isConstOp`, `negArg`) take the table and test for a nil definition where they
  tested the map's second return.

## Result

Driver compile, alternating runs on the same machine:

| Build | Compile |
|---|---|
| main | 31.0 s, 31.8 s |
| this branch | 28.1 s, 28.3 s |

The emitted assembly is byte-identical to main's, so every pass reaches the
same fixpoint on the same functions.

## Tests

- `TestIDTableCoversTheFunctionAndBoundsTheRest`: a fresh table answers zero,
  records what it is given, ignores an invalid value, and neither reads nor
  writes past its end for a value minted after the build.
- `TestUsesIndexBoundsValuesMintedAfterTheBuild`: the counts are right, a nil
  index answers nothing, and a later value reads as unused rather than
  panicking.
- The pass suites in `internal/ssa` cover the folding tables themselves.
