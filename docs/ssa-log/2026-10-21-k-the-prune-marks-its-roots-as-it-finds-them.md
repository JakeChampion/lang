# 2026-10-21 — the prune marks its roots as it finds them

`ssa.prune_dead`, over `prune_live`. Refs #8171.

## The shape

`register_form` prunes dead instructions after its rewriting passes, twice
per function on average. The prune listed every value an effectful
instruction or a terminator reads, one entry per read, and a second walk
marked the list's entries live, skipping the repeats, and pushed each
marked value's own reads. In the per-instruction profile of the
whole-compiler self-compile the three pieces cost 930 M Ir of self time
(the prune's own scan 552 M, the mark 259 M, the root list 120 M).

## What changed

One pass marks a root when it first meets it and pushes only the values
whose definition still has reads to mark, so the work list holds each live
value once and the root list is gone. The live set is the same closure, so
what the prune drops is the same.

## Measured and not landed

Skipping the first prune when none of the passes before it reported a
change: the whole-compiler binary differs and the compile costs 0.7% more.
The lift's output carries dead instructions of its own, so that prune is
not only cleaning up after the passes, and leaving its dead code in place
costs every later pass more than the prune saves.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347:

| subject | main at 42d35e4 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.406 G | 74.257 G (−0.20%) |

The whole-compiler binary is byte-identical to the base compiler's.
