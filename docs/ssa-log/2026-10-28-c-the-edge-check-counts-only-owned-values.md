# 2026-10-28 — the edge check counts only owned values

`ssaunits.holds_invariant` in the unit-plan replay. Refs #8171.

## The shape

The replay checks each edge of a function against the invariant of the
block it enters. `holds_invariant` counted the set bits of the whole
state row, one bit for each value of the function. The self-compile made
242 K such checks, and the count cost about 1.85 G Ir.

## What changed

A replay row sets a bit only for an owned value: `invariant`,
`apply_step` and `enter_phis` each test `owned` first. So the count over
the owned values is the count over the whole row. `ReplayIndex` now
holds a list of the owned values, made once for each function.
`holds_invariant` counts the set bits at those values only.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 8c68804.

| subject | main at 8c68804 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 136.055 G | 134.739 G (−0.97%) |

The whole-compiler binary is byte-identical to the base compiler's.
