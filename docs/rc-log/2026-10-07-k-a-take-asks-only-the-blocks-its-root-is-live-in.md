# 2026-10-07 — a take asks only the blocks its root is live in

`ssaunits.named_after`. Refs #8171.

## Before

A take moves a field or element out of a box `v` whose life goes on. It is
refused when a block after the take reads `v` again. `named_after` answers that
question with a walk forward from the take's block. It visited every reachable
block up to the one defining `v`, and scanned each block's instructions three
times: for a phi taking `v` on the edge it entered by, for the definition, and
for a read.

The walk ignored the liveness it was handed, although both callers ask it only
when `v` is live out of the take's block. On a compile of `checker.fern` it ran
4,123 times, visited 116,351 successor edges and asked `ssa.reads_value`
302,329 times, for 64.7 M Ir in all, 0.45% of the compile.

## Change

The walk keeps to `v`'s live range in the flow it already has:

- a block `v` is not live out of hands it to none of its successors, phi edges
  included, since `ssalive` puts a phi's operand in the live-out set of the
  predecessor on that edge; its successors are not looked at through it;
- a block `v` is not live into has no read of `v` (its uses are in its
  live-in set), and nothing reachable through it reads `v` short of
  its definition, so it is not entered.

Liveness also counts each value's dependencies, so it covers at least the
reads the walk counts, and a block it rules out has none of them. The answer
is unchanged.

## Measured

`checker.fern` to an x86-64 binary under callgrind. Each side's stage 3 is
built by its own stage 2, with no `-g`. Both stage 3s rebuild themselves byte
for byte.

| | main (cdf55f22c) | this change |
|---|--:|--:|
| total Ir | 14,538,423,008 | 14,498,336,306 (−0.28%) |

Byte-identical against a compiler built from main: `checker.fern` for
x86-64, arm64 and wasm32-wasi, and `fern.fern` for x86-64.
