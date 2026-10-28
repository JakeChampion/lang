# 2026-10-28 — the double-claim walk allocates its marks once

`irverifyrc.reaches`, under `double_claim_problems`. Refs #8171.

## The shape

The RC verifier asks `reaches` if one reuse claim can reach a second
claim on the same donor. Each call made its `seen` row with one append
for each operation of the function. The walk itself visits few
operations, so the appends were almost all of the cost. The self-compile
made 5,920 calls, at about 140 K Ir each.

## What changed

`seen` is now a `boolean[]` from `__alloc_bool(ops.len())`, which is
zero from the start.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e, with #13768.

| subject | main at 293eaa55e | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 125.239 G | 124.454 G (−0.63%) |

The whole-compiler binary is byte-identical to the base compiler's.
