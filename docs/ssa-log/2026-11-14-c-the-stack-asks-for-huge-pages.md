# The stack asks for huge pages

2026-11-14. x86-64 and arm64 Linux and Android. Follows
`2026-11-13-g-the-arena-asks-for-huge-pages.md`.

## The shape

The entry code maps a stack of 1 GiB for the program. `cfold` of
`bench/perceus/cfold.fern` recurses 1,000,000 deep with a frame of 128 bytes,
so the workload touches 128 MiB of that stack. It took one page fault for each
4 KiB: 33,835 faults, and about 80 ms of its 250 ms in the kernel.

## The change

After the entry code maps the stack, it calls `madvise` with `MADV_HUGEPAGE`
on the mapping from its guard to 4 MiB below its top (`stack_huge_pages`). A
program that stays in the top 4 MiB keeps small pages. A kernel with no huge
pages refuses the call, and the stack works as before. Darwin does not make
the call.

## Measured

x86-64, this container:

| cfold 20 | before | after | Lean | Koka |
| --- | ---: | ---: | ---: | ---: |
| best CPU time of seven runs, ms | 247 | 180 | 297 | 222 |
| page faults | 33,835 | 2,153 | | |

The Koka time of cfold is with no stack limit. At the default limit of 8 MB
the Koka program stops with a segmentation fault after 83 to 93 ms. An
earlier version of this table gave that time.

A program that prints one line takes 9 page faults before and after, and
2,000 runs of it take the same time.

## Traps

- The frame of `cfold` is still 128 bytes: five saved registers, the frame
  pointer and nine slots. A value that only one arm of the match reads holds
  a saved register for the whole function.
