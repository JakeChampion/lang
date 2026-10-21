# 2026-10-21 — live_at_def marks the reading blocks once

`ssa.live_at_def`, the register allocator's liveness probe. Refs #8171.

## The shape

`live_at_def(r, a)` asks whether r is live where a is defined: a walk of
the blocks reachable from a's block without passing r's. For every block
the walk reached it scanned r's read sites in full to see whether one lay
in that block, so a probe cost the walk's size times r's read count. The
allocator asks it per spill-slot and phi-mate decision; 26 k probes cost
198 M Ir of self time in the per-instruction profile of the whole-compiler
self-compile, mostly in that scan.

## What changed

The blocks holding r's read sites are marked once, in the same scratch
array as the walk's own marks, and each block the walk reaches tests its
mark. The walk, its order and its stopping point are unchanged, so the
answer is the same: true at the first reached block holding a read.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | main at 42d35e4 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.406 G | 74.315 G (−0.12%) |

The whole-compiler binary is byte-identical to the base compiler's.
