# A count of a box type skips bit 0

2026-11-16. x86-64 and arm64. The change is in `ssarc`, the lift and the
emitters.

## The shape

An inline count or uniqueness test made two tests before it read the count
word:

- Bit 0: an odd address is a tagged view (`TestSelfHostLentWindowConsumers`).
- The guard: an address below 0x10000 is null or the index of a variant.

With their branches, these tests ran 8.3% of the instructions of deriv 8 and
8.0% of rbtree-ck 420,000. Most of the tested values were children of a
`Tree` or an `Expr`. A value of a record, or of an enum whose variants are
boxes, has no tag. It is a box, a static box, the index of a variant or null.
Thus it is never an odd address. The guard still finds the index and null.

## The change

- `ir.boxed_count` is a mark 8 on `__fern_rc_inc` and `__fern_rc_is_unique`.
  It says what the mark 4 says, and that the value is never an odd address.
- `aligned_rc` gives mark 8 in place of mark 4 to a value whose type is such
  a record or enum (`never_odd`). The drop helpers give it to each
  uniqueness test of such a type (`aligned_unique_of`).
- The lift keeps the mark. `ssa.aligned_count` is true for mark 4 and for
  mark 8, and each reader of mark 4 reads it.
- `ssadeps.even_values` has each value that a count with mark 8 reads. The
  emitters of x86-64 and arm64 make no test of bit 0 for such a value. A
  sanitizer build keeps the test.

The null that `classified_death` writes in a field of a donor is below the
guard, so the guard alone is correct for it.

## Measured

Cachegrind on x86-64, against the build with the sole releases:

| workload | before | after | change |
|---|---:|---:|---:|
| nqueens 10 | 22,545,714 | 22,378,010 | -0.7% |
| deriv 8 | 101,506,675 | 98,288,549 | -3.2% |
| rbtree 420,000 | 575,618,866 | 574,778,916 | -0.1% |
| rbtree-ck 420,000 | 885,488,621 | 856,883,297 | -3.2% |
| cfold 16 | 30,995,351 | 30,818,519 | -0.6% |

The answers on x86-64, and on arm64 under qemu, are the same. The drop of
`Tree` in `TestSelfHostDropKeepsAStackInItsNodes` had five tests of bit 0 on
x86-64, and now it has none.

## Open

A child that a load of a field read is not null in most drops. Only a donor
of `classified_death` has null fields. A drop helper that no such donor
reaches can skip the guard too.
