# 2026-10-30 — the reuse verifier reads the replay index

`ssaunits.verify_reuse`, `verify_block_tokens` and `verify_carry`.
Refs #8171.

## The shape

The plan verifier builds a `ReplayIndex` that finds the step at each
point of each block. After the replay, `verify_reuse` built a second
index of the same steps with `step_index`. This second index cost about
0.20% of one compile of the compiler.

## What changed

`verify_reuse` takes the `ReplayIndex` of the verifier.
`verify_block_tokens` and `verify_carry` find their steps with
`replayed_step`, which reads that index through `find_step`.

The replay has already made sure that each reachable point has exactly
one step. Thus the two indexes give the same step at each point that the
reuse checks read.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is the schema slice of
2026-10-30-a on main at 39d1bc8d0.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 100.592 G | 100.407 G (−0.18%) |

The whole-compiler binary is byte-identical to the base compiler's.
