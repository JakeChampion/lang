# 2026-10-21 — a block's reads of a value start at a binary search

`ssasem.first_use_in`, under `read_after`, `needed_past_slot`,
`phi_reads_on` and `block_reads` in the unit planner. Refs #8171.

## The shape

`Uses` lists each value's reads in block and position order. Four questions
ask about one block's reads of a value, and each walked every read of the
value to find that block's; `named_after` asks two of them per block its
worklist reaches, so for a value read in many blocks the cost was reads
times blocks. The per-instruction profile put 16 M iterations of those two
loops inside `needed_past_slot`, where they were inlined: most of its 402 M
self cost on the whole compiler's self-compile, eight times the
`checker.fern` figure for a subject five times smaller.

## What changed

`first_use_in` finds the first of a value's reads at or past a block by
binary search over the ordered range, and each question walks from there
while the block matches: the same reads in the same order.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the region-walk slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 75.639 G | 75.474 G (−0.22%) |

The whole-compiler binary is byte-identical to the base compiler's.
