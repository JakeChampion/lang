# 2026-11-13 — a box miss goes to the bump allocator

x86-64 and arm64. `__fern_arr_box` in the two runtimes. Part of #15610.

## The shape

`__fern_arr_box` reads the free list of the size class of the box. When the
list was empty, it called `__fern_alloc` with the size in bytes.
`__fern_alloc` rounded the size, found the size class and read the same list
again. Then it took memory from the end of the heap.

Most boxes of the Perceus workloads take that path. At fold depth 14,
`__fern_arr_box` ran 46,150 times and the list was empty 29,765 times.

## What changed

On x86-64 the miss path calls `.Lalloc_fast_miss`, the label of
`__fern_alloc` after its own read of the list. That label takes the address
of the list, which `__fern_arr_box` has in `rdi`.

On arm64 the miss path calls the new entry `__fern_alloc_bump`. The entry
makes the frame of `__fern_alloc` and continues at `.Lalloc_nofl`.

A box of more than 2,048 bytes keeps the call to `__fern_alloc`. So does the
allocator of a build that counts, traces or checks each allocation. On
x86-64 a hardened build keeps it too.

## Measured

x86-64, Callgrind instructions, against main at `7cbcfda2`. The answers and
the allocation censuses are equal on x86-64, and the answers on arm64 under
qemu, with the default and the hardened profile. Stage 2 and stage 3 of the
compiler are byte-identical.

| Workload | Size | main | this change | |
| --- | ---: | ---: | ---: | ---: |
| rbtree | 100,000 | 155,126,850 | 154,026,828 | -0.71% |
| rbtree-ck | 100,000 | 159,918,595 | 158,818,573 | -0.69% |
| deriv | 6 | 2,754,965 | 2,655,503 | -3.61% |
| nqueens | 8 | 1,165,486 | 1,143,464 | -1.89% |
| cfold | 12 | 2,190,004 | 2,108,604 | -3.72% |

## Not done

A first form took the head of the free list at each site that makes a small
box, with the allocator as the slow path. rbtree rose 0.3% and deriv 0.1%:
a site whose list is empty paid for the test and then for the whole call.
An inline bump allocation at the site can remove the call for each box. It
needs the heap pointer and the heap end at each site, which is two more
addresses on arm64.
