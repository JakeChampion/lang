# 2026-10-21 — splice maps block ids through a table

`seminline.splice`, the semantic inliner's copy of a callee's blocks.
Refs #8171.

## The shape

Copying a callee's blocks into the caller renumbers them from `first`, and
each predecessor and branch target of each copied block was mapped by a scan
of the callee's id list (`index_of`): once per edge, so quadratic in the
callee's block count. On the whole compiler's self-compile the scans were
114 M instructions, ten times the `checker.fern` figure for a subject five
times smaller.

## What changed

`block_positions` builds, once per splice, a table from block id to one
more than its index (ids are dense, `next_block_id` bounds them), and
`position_of` reads it back; an absent id still maps to -1, and a repeated
id to its first block, as the scan answered.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | base (the bare-identifier slice) | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 76.252 G | 76.152 G (−0.13%) |

The whole-compiler binary is byte-identical to the base compiler's.
