# A branch compares the low half

2026-11-15. x86-64. Follows
`2026-11-15-e-the-narrow-fields-of-a-variant-share-a-word.md`.

## The shape

An `i32` lives in a register of 64 bits with its sign extended. A sum or a
difference of two such values needs a wrap to 32 bits, which is a `movslq`.
In `safe` of nqueens, each turn of the loop compares the queen with
`q + diagonal` and with `q - diagonal`. Each compare had its own `movslq`,
and the wrapped value went to no other instruction. `safe` is 68% of the
instructions of nqueens.

## The change

A compare of two values of 32 bits or less gives the same answer on their low
32 bits. `ssa_wrap_fuse` in `compiler/asm_ir.fern` adds the wrap to the
instructions that a branch fuses. The conditions:

- The compare has the width 0 in `imm`, which the lift gives to a compare of
  operands that are not wide and not addresses.
- The compare is `==`, `!=` or a signed order.
- The wrap is `int_cast:i32` or `int_wrap` with the sign, it is next to the
  compare, and the compare is its one reader (`ssa_lone_wrap`).

The fused compare then takes the value before the wrap, and
`ssa_fused_test_emit` writes `cmpl`. `SsaFuse` and `ssaprepare.Fusion` have
the new field `narrow`.

The change is in the emitter, not in the SSA passes. An instruction that the
emitter does not fuse keeps its wrap, so each compare of 64 bits still reads
a value with its sign.

The arm64 backend does not change.

## Measured

Cachegrind instruction counts, x86-64, with the small inputs:

| workload | before | after | change |
| --- | ---: | ---: | ---: |
| nqueens 10 | 25,462,680 | 23,645,994 | -7.1% |

rbtree, rbtree-ck, deriv and cfold do not change. The CPU time of nqueens 13
did not change by more than the noise of this container. The loop follows a
list, and the loads of the list set its time.

The compiler binary has 29,048,752 bytes, down from 29,081,008. Stage 2 and
stage 3 are the same file.

## Traps

- A compiler that reproduces itself is not a proof of this change. Build the
  tree before the change with the new compiler, and run the programs of
  `tests/stdlib` with the two results.
- A compare that becomes a conditional move does not go through the branch
  fusion, and keeps its `movslq`.

What stays open:

- The select fusion and the arm64 backend can do the same.
- The step of the loop counter still has its wrap, because the next turn of
  the loop reads the counter as 64 bits.
