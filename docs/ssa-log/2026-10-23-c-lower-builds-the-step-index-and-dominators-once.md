# 2026-10-23 — lower builds the step index and the dominators once

`ssarc.lower` in the physical lowering. Refs #8171.

## The shape

`ssarc.lower` lowers each function once, 6,787 times per self-compile.
It called `ssaunits.with_reuse`, and that built the plan's step index
and, through `variant_reads`, the dominator tree of the graph. Then
`lower` built the step index again, and `ssalayout.compute` built the
dominator tree again, for the same graph. Each step index cost about
29 K Ir and each dominator tree about 16 K Ir.

## What changed

`lower` builds the step index and the dominator tree first and gives
them to `with_reuse` and `ssalayout.compute`. The step index reads only
the block, the point and the target of each step. `with_reuse` does not
change the steps, and `without_constant_drops` changes only their drops,
so one index serves all three. The report callers in `ssarc` give
`with_reuse` the index they already built.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at ddc5b11.

| subject | main at 4a45ac1 | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 78.626 G | 78.320 G (−0.39%) |

The whole-compiler binary is byte-identical to the base compiler's.

## What is left

`ssadeps.dominates` is still built by `ssabounds.graph_of`,
`semsink.reads_of` (twice for a function that `reuse` and the sinking
both read), `ssasem.operations_error`, `semthread` and
`ssaquotient.shared_quotients`, about 470 M Ir in all.
