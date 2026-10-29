# 2026-10-28 — the grow check solves liveness for its values

`ssaunits.grow_holds`. Refs #8171.

## The shape

`grow_holds` finds the arrays that go whole to a write in place. It
then asks whether each of those arrays is live out of its block. To
answer, it solved liveness over every value of the function, with no
dependencies. The solve cost about 0.5% of the self-compile.

## What changed

`grow_holds` marks the arrays that it asks about. It gives the marks to
`ssalive.compute_checked_kept`, which keeps a slot only for a marked
value. No value has a dependency here, so the liveness of a marked
value does not change. `compute_checked_kept` is now public.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e. The base is main at 753408b4c
with the sink slice of the same pull request.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 104.242 G | 103.993 G (−0.24%) |

The whole-compiler binary is byte-identical to the base compiler's.
