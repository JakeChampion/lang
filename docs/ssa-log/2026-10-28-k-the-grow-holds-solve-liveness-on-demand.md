# 2026-10-28 — the grow holds solve liveness on demand

`ssaunits.grow_holds`, under `semlower.rows_planned`. Refs #8171.

## The shape

`grow_holds` finds the reads to count so that an array goes whole to a
write in place. It solved the `bare` liveness flow of the function first,
on each call. The flow is used only at a grown parameter that no later
instruction reads. Many functions have no such site. The
self-compile made 14,008 solves, at about 763 M Ir. About a quarter of
that cost was for functions with no site.

## What changed

`grow_holds` first collects the candidate sites: the grown parameters
that no later instruction reads. The list depends only on the plan. If
the list is empty, the function returns. Otherwise it solves `bare` and
walks the sites in the same order as before.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e. The base is main at b0241b2f3,
with #13782.

| subject | main at b0241b2f3 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 110.209 G | 109.993 G (−0.20%) |

The whole-compiler binary is byte-identical to the base compiler's.
