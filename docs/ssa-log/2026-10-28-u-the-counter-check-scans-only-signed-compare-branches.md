# 2026-10-28 — the counter check scans only signed-compare branches

`ssa.drop_low_wraps` and `ssa.tested`. Refs #8171.

## The shape

`drop_low_wraps` keeps a wrap on a loop counter only when a branch
bounds the counter. For each wrap that it checks, `tested` scanned
every block of the function for such a branch. Only a block whose
two-way branch tests a signed compare can match. The scans cost about
0.3% of the self-compile.

## What changed

`signed_compare_heads` lists those blocks one time for each
`drop_low_wraps` call, in block order. `tested` scans only the listed
blocks. The order is the same, so `tested` gives the same answer.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 3d1492c39. The base is the same tree with
the replay-row slice of the same pull request.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 104.471 G | 104.273 G (−0.19%) |

The whole-compiler binary is byte-identical to the base compiler's.
