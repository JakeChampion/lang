# 2026-10-29 — lowering takes the dominators that the verifier computed

`ssasem.dominators` and `ssarc.build_lower`. Refs #8171.

## The shape

`ssadeps.dominates` builds the dominator tree of a graph. When the
verifiers are on, `ssadeps.check_graph` builds it as part of
`Analysis.facts`. `ssarc.build_lower` then built it again for the same
graph, to find the boxes that it can use again. Its plan carries the
analysis of that graph.

## What changed

`ssasem.dominators(f, a)` gives the dominator tree from `a.facts` when
the verifier checked the graph. Otherwise it builds the tree. The
lowering and the three array reports use it.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is the settle slice of
the same pull request.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 101.956 G | 101.854 G (−0.10%) |

The whole-compiler binary is byte-identical to the base compiler's.
