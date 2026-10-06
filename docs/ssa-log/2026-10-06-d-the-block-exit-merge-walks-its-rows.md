# 2026-10-06 — the block-exit merge walks its rows, not the slot range

`ssa_lift.lift_impl`, every target. Refs #8171.

## The shape

At a `block`'s end the lift merges the slots written since the block
opened: each out-edge's value for a slot is what the edge recorded, else the
value at the open, and a phi gathers them when they disagree. The phis go in
slot order, so the merge scanned every slot from the lowest written to the
highest. On `checker.fern` that was 8.72 M slot steps over 36,964 merges
for 2.96 M written rows, and 61,228 of those rows produced a phi.

The rows that agree touch only their own slot, so their order does not
matter. The merge now walks its rows as they stand, settles the ones that
agree on the spot, and keeps the few that need a phi in a list ascending by
slot, inserting each as it is found. The phis are then emitted from that
list, numbered in slot order as before.

A block no edge leaves has one incoming value per slot, the fall-through
one, so its merge changed nothing: it built the edge matrix, marked every
row and restored every slot to itself. It is skipped. 20,651 edges leave
the 36,964 merged blocks, so most of them had none.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at 5001ec0d against this branch. The two compilers build
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern`, to byte-identical
binaries, as do the native-built pair. `scripts/selfhost-emit-hashes`
matches on all 2,004 rows:

| | main | this branch |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 18.730 G | 18.494 G (−1.26%) |
| `ssa_lift.lift_impl`, self | 1,210 M | 972 M |
| `ssa_lift.lift_impl`, inclusive | 1,506 M | 1,270 M |

## The trap

The counts above came from a `--dump-instr=yes` run resolved through
`addr2line` on the `-g` stage-2 binary, which works: its `.debug_line`
carries one row per statement. What it charges to a line is not always that
line's work. The parallel moves the SSA emitter places on a merge edge carry
the position of the last statement before them, so the main loop's back
edge put 130 M on `ok = false;`, a line that never runs, and the `br` arm's
merge put 88 M on the two appends beside it. Read a per-line table for its
loops, and read a cost on a line that cannot have run as the move set of
the merge next to it.
