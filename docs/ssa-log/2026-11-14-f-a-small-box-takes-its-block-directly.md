# A small box takes its block directly

2026-11-14. x86-64 and arm64. Follows
`2026-11-14-e-the-shape-of-a-box-is-a-tag-in-its-header.md`.

## The shape

`__fern_rec_box` was a wrapper. It called `__fern_arr_box`, which wrote the
header of an array, and then wrote the header of a box over it. A free list
hit ran 30 instructions and two calls. A bump ran more, through a third call
to `.Lalloc_fast_miss`. `__fn___fern_box_dec` was a wrapper too. For a unique
box it wrote the header of an array, and `__fn___fern_arr_dec.u` then read
that header again.

Callgrind, with the small inputs, inclusive cost of the two routines:

| workload | `__fern_rec_box` | `__fn___fern_box_dec` |
| --- | ---: | ---: |
| nqueens 10 | 8.7% | 6.1% |
| deriv 8 | 15.7% | 10.1% |
| cfold 16 | 17.6% | 11.7% |
| rbtree-ck 420,000 | 10.0% | 6.0% |
| rbtree 420,000 | 2.5% | 1.5% |

## The change

A box of 2 to 255 words has a block of n + 1 words. Its free list is at
`__fern_freelist + 8 * (n + 1)`.

- `__fern_rec_box` takes the head of that list. When the list is empty, it
  moves the bump pointer by the block. It writes the header of one word and
  returns. Only `%rax` changes on x86-64. On arm64 `x1` to `x3` change, as in
  `__fern_arr_box`, and `x4` goes back to its value.
- `__fn___fern_box_dec.u` puts a unique box of 2 to 255 words on that list.
- Each other box, a bump that crosses a 64 KiB line or the end of the heap,
  and a heap that is not mapped, take the old path. The allocator colours a
  line and maps the heap there.

The direct paths are off in each build that counts allocations: the leak
census, an allocation profile, and a program that reads
`__heap_alloc_count` (`direct_boxes`). The builds that keep the header of an
array keep them off too.

`compiler/ssaboxroutines.fern` has the new lines of the two routines, and
`compiler/drivers/ssaboxroutinecontrols.fern` changes each new line and
requires a refusal.

## Measured

x86-64, Cachegrind, against `517825ab`:

| workload | Ir before | Ir after | change |
| --- | ---: | ---: | ---: |
| rbtree 420,000 | 713,668,992 | 700,660,159 | -1.8% |
| rbtree-ck 420,000 | 1,088,285,676 | 1,007,838,526 | -7.4% |
| deriv 8 | 115,407,825 | 104,899,844 | -9.1% |
| nqueens 10 | 29,049,322 | 27,079,325 | -6.8% |
| cfold 16 | 40,040,954 | 36,696,411 | -8.3% |

The D1 misses do not change. The answers are equal on x86-64 and on arm64
under qemu.

## Traps

- The times of one run on this container move by more than these changes.
  Trust the counts of Cachegrind.
- Each construction still calls `__fern_rec_box`. An allocation in the body
  of the constructor removes the call, but `ssaconstructormachine` then must
  follow three paths: the free list, the bump and the call.
- A box of one word still takes the old path, because it keeps the block of
  an array: three words, of which it uses two. `Val`, `Var` and `Ln` of
  deriv and `Val` and `Var` of cfold are such boxes: 80,721 calls of
  `__fern_arr_box` on deriv 8. A block of two words for them is the next gap
  in size. `ssasmallheap.capacity_of` then changes too.
