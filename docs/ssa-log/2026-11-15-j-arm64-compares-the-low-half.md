# arm64 compares the low half

2026-11-15. arm64. It continues
`2026-11-15-f-a-branch-compares-the-low-half.md`, which made the rule
for x86-64.

## The shape

`safe` of nqueens compares an `i32` with a sum and with a difference of
two `i32` values. The arm64 backend extended the sign of each result to
64 bits and compared the 64-bit registers:

```
add x11, x12, x9
sxtw x11, w11
cmp x0, x11
```

## The change

A branch on such a compare now reads the low 32 bits:

```
add x11, x12, x9
cmp w0, w11
```

The rule is the x86-64 rule. A wrap to 32 bits directly before the
compare, which gives its value to no other instruction, has no effect.
It applies to the equality and the signed order conditions only. A
compare of the low halves with the sign gives the same answer as a
compare of the two values with the sign extended.

The rule is now in `asmcore.narrow_compare`, and the two backends call
it. `asmcore.before_immediates` and `asmcore.immediate_inst` move there
too, because the rule reads them. The x86-64 output does not change.

## A copy of a loop test

A block that jumps back to the head of a loop can end with a copy of the
test of the head. This occurs only when the head has no other instruction. On arm64,
`ssa_rotates` counted the instructions of the head that are not constants,
and compared the count with the length of the fused test. The fused test
now covers a constant: the wrap, then the constant, then the compare.
Thus a head with one more instruction gave the same count.

`spectral.huffman` of `std/audio/mp3` has the head
`add x9, x4, #4` and `cmp w9, #576`. The copy compared `w9` without the
`add`, and the decoder gave wrong PCM. `ssa_rotates` now reads the
instructions before the fused test, as on x86-64.

## Measured

arm64 instructions of the `bench/perceus_*` programs, counted with the
translation-block log of qemu:

| program | before | after |
|---|---|---|
| perceus_nqueens | 88.42 M | 84.87 M |
| perceus_rbtree | 30.16 M | 30.16 M |
| perceus_rbtree_ck | 41.71 M | 41.71 M |
| perceus_cfold | 53.83 M | 53.83 M |
| perceus_deriv | 76.88 M | 76.88 M |

nqueens runs 4.0% fewer instructions. Each program gives the same
checksum as before.

## Tests

`TestSelfHostBranchComparesTheLowHalf` now checks the arm64 listing as
well: a compare of `w` registers and no `sxtw`. Its program runs on
three targets, with sums and differences that wrap at the two ends of
the range of `i32`. Its new loop `steps` adds before it compares in the
head. Without the change to `ssa_rotates` it gives exit 5 on arm64.
