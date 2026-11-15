# arm64 tests a tag without building it

2026-11-15. arm64.

## The shape

A test of a variant loads the header word of the box and compares it
with the tag of the shape. A tag is the index of the variant in the high
16 bits and the count of words in the low 16 bits. An arm64 `cmp` takes
an immediate of 12 bits, so the backend built the tag in a register
first:

```
ldr w11, [x10, #-4]
movz w12, #1
movk w12, #1, lsl #16
cmp x11, x12
```

The loop of `safe` in nqueens does this one time for each iteration.

## The change

An equality test now takes in the instruction that makes the tag. The
instruction must be the only reader of the tag. For a branch, it must be
directly before the compare. For a select, constants for the select can
come between them. The test is then a subtraction of the high 12 bits and a
compare with the low 12 bits:

```
ldr w11, [x10, #-4]
sub x5, x11, #16, lsl #12
cmp x5, #1
```

The two compares are equal modulo 2^64, so the equality and the
inequality are the same. A tag below 4096 is a `cmp` with one immediate.
A tag of more than 24 bits still goes through a register, `x5`. A tag
has more than 24 bits only when the enum has more than 255 variants.

## Measured

arm64 instructions of the `bench/perceus_*` programs, counted with the
translation-block log of qemu:

| program | before | after |
|---|---|---|
| perceus_rbtree | 31.10 M | 29.98 M |
| perceus_rbtree_ck | 45.05 M | 43.64 M |
| perceus_cfold | 58.44 M | 56.10 M |
| perceus_deriv | 83.94 M | 78.20 M |
| perceus_nqueens | 94.06 M | 91.02 M |

Each program gives the same checksum. The x86-64 output does not change.

## Tests

`TestSelfHostArm64ShapeTestTakesTheTag` checks that a branch and a select
on a tag test subtract and compare, and do not build the tag. Its
program tests each variant of an enum with four shapes, on three targets.
