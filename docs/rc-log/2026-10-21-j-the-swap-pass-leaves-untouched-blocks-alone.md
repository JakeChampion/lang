# 2026-10-21 — the swap pass leaves untouched blocks alone

`seminline.swapped_blocks`, under `splice` and `renamed`. Refs #8171.

## The shape

After each splice the inliner renames the call's result to the callee's
returned value in every use, and did so by rebuilding every block of the
function, instruction by instruction, whether or not the block named a
swapped value. One splice per inlined call, each over the whole function:
268 M instructions on the whole compiler's self-compile across
`swapped_blocks`, `swapped_inst` and `swapped_all`, eight times the
`checker.fern` figure for a subject five times smaller.

## What changed

`block_swaps_any` asks, without copying, whether any operand, record-update
base, or terminator value of a block is in the swap; a block none is in is
kept as it is. The rebuilt blocks are the same as before.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (main at c22d9df) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.861 G | 74.595 G (−0.36%) |

The whole-compiler binary is byte-identical to the base compiler's.
