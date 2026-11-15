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

A box of 1 to 255 words has a block of n + 1 words. Its free list is at
`__fern_freelist + 8 * (n + 1)`.

- `__fern_rec_box` takes the head of that list. When the list is empty, it
  moves the bump pointer by the block. It writes the header of one word and
  returns. Only `%rax` changes on x86-64. On arm64 `x1` to `x3` change, as in
  `__fern_arr_box`, and `x4` goes back to its value.
- A bump that crosses a 64 KiB line or the end of the heap, and a heap that
  is not mapped, ask `__fern_alloc` for the block. The allocator colours a
  line and maps the heap there.
- `__fn___fern_box_dec.u` puts a unique box of 1 to 255 words on its list.
- A box of more words takes the old path.

A box of one word had the block of an array: three words, of which it used
two. It now has a block of two words, as in Koka. `Val`, `Var` and `Ln` of
deriv and `Val` and `Var` of cfold are such boxes.

The direct paths are off in each build that counts allocations: the leak
census, an allocation profile, and a program that reads
`__heap_alloc_count` (`direct_boxes`). The builds that keep the header of an
array keep them off too. In those builds a box of one word keeps the block
of an array.

The models:

- `compiler/ssaboxroutines.fern` has the new lines of the two routines, and
  `compiler/drivers/ssaboxroutinecontrols.fern` changes each new line and
  requires a refusal.
- `ssasmallheap.capacity_of` gives a box of one word a block of two words.
- `ssasmallcallermachine` gives `%rcx` the number of words of the box after a
  release, as the direct path leaves it.

## Measured

x86-64, Cachegrind, with the small inputs, against `517825ab`:

| workload | Ir before | Ir after | change | D1 misses before | D1 misses after |
| --- | ---: | ---: | ---: | ---: | ---: |
| rbtree 420,000 | 713,668,999 | 700,656,129 | -1.8% | 949,196 | 949,202 |
| rbtree-ck 420,000 | 1,088,285,683 | 1,007,814,021 | -7.4% | 4,807,990 | 4,807,989 |
| deriv 8 | 115,564,178 | 101,755,298 | -11.9% | 490,971 | 473,953 |
| nqueens 10 | 29,049,329 | 27,079,169 | -6.8% | 61,140 | 61,136 |
| cfold 16 | 40,040,961 | 34,586,659 | -13.6% | 604,454 | 547,794 |

x86-64, this container, with the full inputs and no stack limit. Best CPU
time of nine runs of each program, with the two sets in turn, and peak
resident memory:

| workload | before, ms | after, ms | change | memory before | memory after |
| --- | ---: | ---: | ---: | ---: | ---: |
| rbtree 4,200,000 | 870 | 850 | -2.3% | 194 MB | 194 MB |
| rbtree-ck 4,200,000 | 2761 | 2663 | -3.6% | 1376 MB | 1376 MB |
| deriv 10 | 1040 | 957 | -7.9% | 438 MB | 420 MB |
| nqueens 13 | 866 | 859 | -0.8% | 94 MB | 94 MB |
| cfold 20 | 144 | 142 | -1.7% | 176 MB | 168 MB |

The answers are equal on x86-64 and on arm64 under qemu. Stage 2 of the
compiler equals stage 3.

## Traps

- The times of one run on this container move by more than these changes.
  Trust the counts of Cachegrind.
- Each construction still calls `__fern_rec_box`. An allocation in the body
  of the constructor removes the call, but `ssaconstructormachine` then must
  follow three paths: the free list, the bump and the call.
- A block of two words is also the block of a raw allocation of 16 bytes.
  The two share one free list.
