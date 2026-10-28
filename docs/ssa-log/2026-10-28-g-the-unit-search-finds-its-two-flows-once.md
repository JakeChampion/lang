# 2026-10-28 — the unit search finds its two flows once

`ssaunits.units_of`, `held_elements` and `twin_holds`. Refs #8171.

## The shape

`held_elements` solved two liveness flows: `direct`, over the parents
of the analysis, and `bare`, with no dependencies. `twin_holds` solved
`bare` again. When `units_of` adds a payload take, it calls
`held_elements` a second time, which solves both flows again. All these
flows come from the same graph and the same analysis.

## What changed

`units_of` solves `direct` and `bare` once and gives them to
`held_elements` and `twin_holds`.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e, with #13768.

| subject | main at 293eaa55e | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 125.239 G | 124.360 G (−0.70%) |

The whole-compiler binary is byte-identical to the base compiler's.
