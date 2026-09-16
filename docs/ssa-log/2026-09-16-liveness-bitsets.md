# Measured 2026-09-16: the liveness fixpoint on bitsets

`2026-09-16-width-resolver-worklist.md` left `ComputeLivenessWithDependencies`
as the SSA layer's largest remaining entry in the profile of the x86-64 SSA
build of the self-hosted driver: 13.6 s cumulative, 19% of the whole once
the width resolver was out of the way, and nearly all of it in Go map
access (`mapaccess1_fast32` 46%, `mapIterNext` 25%) from the backward
dataflow walking one map entry at a time.

**What changed.** `internal/ssa/liveness.go` runs the fixpoint on one
bitset per block, so a successor's live-in set joins a block's live-out
set a word at a time, and the phi-result and def subtractions are a
word-wise and-not. The map-shaped `LiveIn` / `LiveOut` the register
allocator and the self-host lifetime analysis read are built once from
the final sets. The result is the same set of values; the new test pins a
loop with more values live across it than one word holds.

Best of one, the 4-core container, the driver built with
`fern -target x86-64-linux -backend ssa examples/self_host/asm_ir_run.fern`:

| build | compile |
| --- | --- |
| main (`385aeeb04`) | 91.7 s |
| bitset liveness alone | 82.5 s |
| width worklist (#9453) alone | 54.6 s |
| both | 40.6 s |

The binary is byte-for-byte identical in all four builds. Against the
flat backend's 18.5 s the SSA path now costs 2.2x where it cost 5x this
morning; the remaining gap has no single owner in the profile, with the
garbage collector, the parser and the assembler's line parsing each
taking a few seconds that the flat backend shares.
