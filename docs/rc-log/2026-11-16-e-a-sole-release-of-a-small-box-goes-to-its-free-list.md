# A sole release of a small box goes to its free list

2026-11-16. x86-64 and arm64. The change is in `ssarc`, the lift and the
emitters.

## The shape

A drop that proved a box unique called `__fn___fern_box_dec.u` to free it.
That routine made these tests again for each box:

- The high half of the header has a number of words, so the block is not an
  array.
- The count is 1.
- The number of words is in the range 1 to 255.

The drop knew each answer. Its uniqueness test read the count, and the type
of the box gave the other two.

## The change

- `ir.sole_box` is a mark 9 on `__fern_rc_dec`. It says what `ir.sole` says,
  and that the box has 1 to 255 words and is not an array.
- `sole_release_of` gives that mark when each variant of the enum has at most
  255 fields, and the enum has no finalizer (`small_boxes`). The four sole
  releases of the chain drop and of the stack drop use it.
- The lift keeps the mark as `ssa.SOLE_BOX`. `ssa.sole_release` is true for
  the two marks, and each reader of mark 5 reads it.
- The emitters call `__fn___fern_box_free.u` for the mark. That routine reads
  the number of words n from the header and puts the block on the free list
  of n + 1 words. The output has the routine only when a call names it.

A build whose box routines are not direct keeps `__fn___fern_box_dec.u`: the
routine jumps to it, or the emitter calls it.

## Measured

Cachegrind on x86-64, against the build with the count that skips bit 0:

| workload | before | after | change |
|---|---:|---:|---:|
| nqueens 10 | 22,378,004 | 22,218,662 | -0.7% |
| deriv 8 | 98,288,543 | 95,903,544 | -2.4% |
| rbtree 420,000 | 556,682,322 | 556,682,328 | 0.0% |
| rbtree-ck 420,000 | 844,633,400 | 830,568,600 | -1.7% |
| cfold 16 | 30,818,513 | 30,578,293 | -0.8% |

The answers on x86-64, and on arm64 under qemu, are the same.

## Open

The routine reads the number of words from the header. A release that knows
the variant can name the free list as a constant, as `__fern_rec_box_n` does.
The sole release of `classified_death` frees a record or a variant of a known
shape, and it still calls `__fn___fern_box_dec.u`.
