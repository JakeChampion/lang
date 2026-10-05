# 2026-10-05 — a function with no range loop skips range desugaring

Self-host parser, every target. Refs #8171.

## The shape

`parser.desugar_prepass_module` rewrites each `for i in LOW..HIGH` loop into
a `while`. `desugar_ranges_block` did that by rebuilding every statement of
every function body, and descending into each lambda, to reach the range loops.
Most functions have none, so for them it rebuilt the body to change nothing.
The self-host compiler's own sources have none at all, so the measurement
below is all skip path.

`has_range_for` walks the body once, read-only, over astwalk's fold spine,
which also covers lambda bodies. It asks whether any `for` iterates a range.
A body with none keeps its statements as parsed. `desugar_ranges_func`, the
interpreter's route, takes the same check.

The rebuild resets each lowered match arm's `alt_cont` to `false`. That field
is set only on the written arms in `StmtMatch.sugar`, which the rebuild carries
through untouched, so skipping it changes nothing.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at c819bee2 against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,001 rows:

| | main | ranges only where written |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 17.853 G | 17.824 G (−0.16%) |
| `desugar_prepass_module`, inclusive | 75.6 M | 54.6 M |

A body that has a range loop pays for the walk as well as the rebuild.
`conformance/cases/elementary_automaton` has five range loops, and its compile
moves from 101.167 M to 101.171 M Ir (+0.003%), with an identical binary.

## A bare `defer` of a range loop

Writing the tests for each place a range loop can sit found that the
self-host parser accepted `defer for i in 0..4 { ... }`, which the native
parser refuses, and then failed in the lowering with "call target has no
semantic contract: __range". The self-host parser now takes only a block, an
expression or an assignment after `defer`, as native does (#11602), so a
range loop in a `defer` sits in a block.

`conformance/cases/range_for_nested` covers a range loop in each `if` branch,
a `match` arm and a `defer` block, and the interpreter driver has the same
shapes.
