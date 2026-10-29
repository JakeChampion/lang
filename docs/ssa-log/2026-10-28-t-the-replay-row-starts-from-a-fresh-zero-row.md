# 2026-10-28 — the replay row starts from a fresh zero row

`ssaunits.invariant`. Refs #8171.

## The shape

The unit-plan replay makes the entry row of each block in `invariant`.
The row started as `flow.none`, the zero row that the flow shares. The
first bit that the replay set made a copy of the shared row. The copy
cost about 1,500 Ir for each block, and about 0.2% of the self-compile.

## What changed

`invariant` starts from a new zero row from `__alloc_bool`. The row
has no other owner, so each bit is set in place.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39, because main now refuses the old
subject. The base is the same tree.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 104.571 G | 104.471 G (−0.10%) |

The whole-compiler binary is byte-identical to the base compiler's.
