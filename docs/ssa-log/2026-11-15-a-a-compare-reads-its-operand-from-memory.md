# A compare reads its operand from memory

2026-11-15. x86-64. Follows
`2026-11-14-e-the-shape-of-a-box-is-a-tag-in-its-header.md`.

## The shape

A `match` loaded the tag of a box to a register and then compared the
register with a constant. A loop loaded the length of an array and then
compared its counter with the register. Each pair was two instructions, and
the loaded value went to no other instruction.

On rbtree 420,000, the insert function ran 10.9 million tag tests on its
first `match` and 10.5 million on the colour.

## The change

`ssa_load_fuse` in `compiler/asm_ir.fern` adds the load to the instructions
that a branch fuses. The conditions:

- The load is an `LD_AT`, and the compare is its one use.
- Only immediates are between the load and the compare. `SHAPE_ADDR` counts
  as an immediate here, because the tag of a shape is a constant.
- The compare is an integer compare at the end of a block, and the branch is
  its one use.

The emitted compare (`ssa_load_compare`):

- A loaded left operand and an immediate: `cmpq $imm, off(base)`. For a load
  of 32 bits it is `cmpl`, only for `==` and `!=`, and only for an immediate
  from 0 to 2147483647.
- A loaded left operand and a register: `cmpq %reg, off(base)`.
- A loaded right operand: `cmpq off(base), %reg`.

`SsaFuse` and `ssaprepare.Fusion` have three new fields: `load_bits`,
`load_at` and `load_right`. The base of the load is in `a`, or in `b` when
`load_right` is set.

The arm64 backend does not change. It has no compare with a memory operand.

## Measured

Cachegrind instruction counts, x86-64, with the small inputs:

| workload | before | after | change |
| --- | ---: | ---: | ---: |
| rbtree 420,000 | 713,669,473 | 660,042,425 | -7.5% |
| rbtree-ck 420,000 | 1,088,286,175 | 1,026,349,290 | -5.7% |
| deriv 8 | 115,564,670 | 112,697,949 | -2.5% |
| cfold 16 | 40,041,453 | 38,884,681 | -2.9% |
| nqueens 10 | 29,049,821 | 27,816,528 | -4.2% |

Koka runs 593,463,215 instructions on rbtree 420,000.

The best CPU time of seven runs did not change by more than the noise of
this container. The processor runs a compare with a memory operand as the
same two operations. The gain is the count, the size of the code and one
free register.

The compiler binary has 29,012,400 bytes, down from 29,029,920. Stage 2 and
stage 3 are the same file.

## Traps

- A compare of a zero-extended load of 32 bits is a compare of 64 bits. An
  ordered compare of 32 bits gives other flags for a value with bit 31 set.
- `ssa_before_immediates` did not skip `SHAPE_ADDR`. The first version of
  this change then found no load before a tag test.
- A select (`cmov`) has its own fusion (`ssa_select_fuse`), and that one
  does not take a load.

What stays open:

- A red-black `Node` has 48 bytes. The Koka node has 32, because Koka keeps
  the `int32` key, the colour and the boolean in one word. With 4,200,000
  nodes Fern uses 194 MB and Koka uses 133 MB. This is the largest part of
  the time gap on rbtree and rbtree-ck.
- The colour of a node is a pointer to a constant box. Each colour test
  loads the pointer and then reads the tag of that box.
