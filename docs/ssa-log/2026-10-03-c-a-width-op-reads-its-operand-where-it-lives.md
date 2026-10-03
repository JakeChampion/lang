# 2026-10-03 — a width op reads its operand where it lives

`asm_ir.ssa_unary_from`, `asm_arm64_ir.ssa_unary_from`. Refs #8171.

## The shape

The register path selected every width op, `not` and bit count in place on
the destination, so an operand living in another register was copied there
first:

```
movq %r8, %rsi
movslq %esi, %rsi
```

```
mov x9, x11
sxtw x9, w9
```

The sign extension that wraps an i32 sum into a loop accumulator's home is
the common case: the sum's register is free to take the result, but the phi
home is not, so the copy runs every round. Main's stage-2 compiler holds
3,511 of these pairs, and their copies alone cost 120 M Ir on the compile of
`checker.fern`.

## What changed

When the operand lives in a register other than the destination, the op reads
it there: `movslq %r8d, %rsi`, `sxtw x9, w11`. The same goes for the
unsigned 32-bit wrap (`movl` / `mov w`), the i32 load (`movslq (%r8), %rsi` /
`ldrsw`), and the leading and trailing zero counts. On x86-64 that includes
`popcnt` too, and on arm64 `not` (`cmp; cset`) and the u8 cast (`and`) as
well. A spilled operand, an operand already in the destination, and the ops
with no two-register form keep the copy and the in-place form.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
built from main at 9b7dc74d and from this change on it:

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 25.444 G | 25.321 G (−0.48%) |
| copy-then-extend pairs in the stage-2 binary | 3,511 | 23 |

Emitted bytes change on 672 of the 1,965 `selfhost-emit-hashes` rows (340
x86-64, 332 arm64); wasm32 is untouched. arm64 is unmeasured, since there is no
instruction counter under qemu here.

## Witnessed

`TestSelfHostOptimisationShapes` gains `wrap_reads_its_source`, whose
forbidden copy-then-extend main emits on both targets.
`TestSelfHostRedundantWrapFlowsThrough`, `TestSelfHostSSA*`,
`TestSelfHostX86*` and `TestSelfHostArm64Asm*` (565 passing, no skips), the
stage-2 build and its compile of `checker.fern`, the emit-hash sweep, the lint
ratchet and `make fmt-check`.
