# 2026-10-06 — block lookups by position in the rc planner and emitter

Self-host rc planning, every target. Refs #8171.

## The shape

Three lookups found a block by scanning a list, once per use:

- `ssaunits.unique_receivers` found each plan step's block with
  `ssa.block_index`, a scan of the block list: 121,415 scans on
  `checker.fern`, about 830 instructions each.
- `ssarc.edge` found each edge's target the same way, through a private copy
  of `block_index`.
- `ssarc.jump` found the target's place in the layout order with `rank`, a
  scan of the order.

Each now indexes a table built once per function: `ssa.block_positions` for
the first two, and `order_ranks` for the third, which keeps a block's first
position in the order as `rank` did. `ssa.block_index`, its `ssarc` copy and
`rank` are gone.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at ac29eede9 against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern`, to byte-identical
binaries. `scripts/selfhost-emit-hashes` matches on all 2,004 rows:

| | main | by position |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 20.961 G | 20.798 G (−0.78%) |
| `ssaunits.unique_receivers`, inclusive | 125 M | 29 M |
| `ssarc.jump`, inclusive | 131 M | 61 M |
