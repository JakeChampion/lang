# A box that a match takes apart goes to its free list

2026-11-16. x86-64 and arm64. The change is in `ssarc`. It continues
`2026-11-16-e-a-sole-release-of-a-small-box-goes-to-its-free-list.md`.

## The shape

A match or a field read can take a box with one count apart. The fields that
the function uses go on, the other fields drop, and the box itself is freed
(`classified_death`). That free had the mark `ir.sole`, so it called
`__fn___fern_box_dec.u`. The routine tested again that the block is a box,
that its count is 1, and that it has 1 to 255 words.

`__fn___fern_box_dec` ran 8.6% of the instructions of nqueens and 8.8% of
cfold.

## The change

- The free of `classified_death` uses `sole_release_of`, as the frees of the
  chain drop do. A box of a small type goes to `__fn___fern_box_free.u`.
- `small_boxes` is also true for a record with 1 to 255 fields and no
  finalizer. A record has the same box as a variant. A handle and the
  record of a closure stay with the general release.
- `sole_release` has no caller, and it is gone.

## Measured

Cachegrind on x86-64, the bench corpus, against the build with the drop loop
that keeps no flag:

| row | before | after | change |
|---|---:|---:|---:|
| perceus_nqueens | 56,513,675 | 55,024,075 | -2.6% |
| perceus_cfold | 39,487,737 | 38,773,737 | -1.8% |
| perceus_rbtree | 24,934,874 | 24,647,034 | -1.2% |
| perceus_rbtree_ck | 31,885,855 | 31,539,775 | -1.1% |
| perceus_deriv | 51,941,541 | 51,571,941 | -0.7% |

`TestSelfHostTakenApartBoxGoesToItsFreeList` takes a record and a variant
apart. Each function calls the direct free and not
`__fn___fern_box_dec.u`, on x86-64 and on arm64. The census is balanced with
the sanitizer on three targets.

## Open

The routine reads the number of words from the header. A routine for each
size, as `__fern_rec_box_n` is, saves three instructions for each free.

An inline free saves six. But a build can stop the direct routines after the
emitter wrote the function, with the need `alloc_count`. Thus the free must
stay a call.
