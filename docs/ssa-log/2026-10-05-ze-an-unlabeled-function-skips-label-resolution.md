# 2026-10-05 — a function with no labeled jump skips label resolution

Self-host parser, every target. Refs #8171.

## The shape

`parser.resolve_labels_module` sets the `tag` of each labeled `break` and
`continue` to the loop depth of its target. It ran on every function of every
parsed module, and `resolve_labels_stmt` rebuilds each statement and
expression it passes on the way to a jump. Most functions name no label, so
for them it rebuilt the whole body to change nothing.

## The change

`has_labeled_jump` walks a function's body once, reading only. It asks
whether any `break` or `continue`, lambda bodies included, names a label. A
function with none keeps its body as parsed. The top-level statements are
resolved as before.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at 40ceed58 against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm to byte-identical binaries.
`scripts/selfhost-emit-hashes` matches on all 2,001 rows, which include the
ten conformance cases with a labeled jump:

| | main | labels only where named |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 18.087 G | 17.937 G (−0.83%) |
| `resolve_labels_module`, inclusive | 105 M | 17 M |
| `desugar_prepass_module`, inclusive | 136 M | 76 M |
