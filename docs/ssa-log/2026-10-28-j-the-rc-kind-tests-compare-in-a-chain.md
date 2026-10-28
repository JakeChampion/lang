# 2026-10-28 — the RC kind tests compare in a chain

`ssarc.builds`, `container_op` and four more kind tests. Refs #8171.

## The shape

Six kind tests in `ssarc` gave a list of kinds to `kind_in`, which
looped over the list. The loop line cost about 345 M Ir in the
self-compile.

## What changed

Each test is now a chain of `k == ...` comparisons, in the style of
`ssasem.is_cell_op`. `kind_in` is deleted.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e, with #13768.

| subject | main at 293eaa55e | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 125.239 G | 124.964 G (−0.22%) |

The whole-compiler binary is byte-identical to the base compiler's.
