# 2026-10-28 — the sink solves liveness for its tree

`semsink.reads_of`. Refs #8171.

## The shape

`reads_of` solved liveness over every value of the function. The sink
asks about the liveness of only two kinds of value: a counted projection
and the parent of a counted projection. The projection tree marks the
same values in `parent`. The solve cost about 0.84% of the self-compile.

## What changed

`reads_of` gives `parent` to `ssalive.compute_kept`. The solve keeps a
slot only for a marked value. No value has a dependency here, so the
liveness of a marked value does not change.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e. The base is main at 903c9a490
with the graph-check slice of the same pull request.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 104.267 G | 104.016 G (−0.24%) |

The whole-compiler binary is byte-identical to the base compiler's.
