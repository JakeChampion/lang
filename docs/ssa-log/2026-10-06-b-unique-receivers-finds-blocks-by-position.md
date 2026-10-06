# 2026-10-06 — unique receivers finds blocks by position

Self-host rc planning, every target. Refs #8171.

## The shape

`ssaunits.unique_receivers` reads the instruction at each step of a
function's plan. It found the step's block with `ssa.block_index`, a scan of
the block list, so a function with many blocks and many steps paid for both:
121,415 scans on `checker.fern`, about 830 instructions each. It now takes
the function's block positions once (`ssa.block_positions`) and indexes them.
`ssa.block_index` had no other caller and is gone.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at 21b61ba6a against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,004 rows:

| | main | by position |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.959 G | 20.863 G (−0.46%) |
| `ssaunits.unique_receivers`, inclusive | 125 M | 29 M |
