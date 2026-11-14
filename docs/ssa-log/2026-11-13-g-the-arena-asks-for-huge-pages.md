# The arena asks for huge pages

2026-11-13. x86-64 and arm64 Linux and Android. Follows
`2026-11-13-f-the-bump-allocator-colours-each-64k-line.md`.

## The shape

A Fern program took one page fault for each 4 KiB of heap that it touched.
The Koka programs of `bench/perceus` took a few hundred faults in all: the
Koka allocator asks the kernel for transparent huge pages. On this host the
kernel gives them only to memory that asks (`madvise` mode).

Page faults and system time, x86-64, before this change:

| workload | Fern faults | Fern sys ms | Koka faults | Koka sys ms |
| --- | ---: | ---: | ---: | ---: |
| rbtree 4,200,000 | 74,384 | 140 | 189 | 24 |
| rbtree-ck 4,200,000 | 530,499 | 824 | 732 | 204 |
| deriv 10 | 219,234 | 320 | 367 | 60 |
| nqueens 13 | 47,904 | 88 | 170 | 40 |

## The change

The first allocation maps the arena. It now calls `madvise` with
`MADV_HUGEPAGE` on the arena from 4 MiB past its base to its end. A program
that stays in the first 4 MiB keeps small pages. A small tool then does not
pay to clear 2 MiB at its first allocation. A kernel with no huge pages
refuses the call, and the arena works as before. Darwin does not make the
call.

The machine model of the first allocation
(`compiler/ssaallocatorfirstmachine.fern`) takes the one request after the
call that maps the arena, with its four arguments.

## Measured

x86-64, this container, one run each, before and after:

| workload | wall ms | faults | sys ms |
| --- | ---: | ---: | ---: |
| rbtree 4,200,000 | 988 to 891 | 74,384 to 1,179 | 140 to 60 |
| rbtree-ck 4,200,000 | 3318 to 2928 | 530,499 to 2,071 | 824 to 686 |
| deriv 10 | 1594 to 1421 | 219,234 to 1,467 | 320 to 147 |
| cfold 20 | 312 to 296 | 53,821 to 33,842 | 80 to 72 |
| nqueens 13 | 1009 to 863 | 47,904 to 1,126 | 88 to 24 |

## Traps

- The faults that cfold keeps are not in the arena. The program maps its own
  stack of 1 GiB, and cfold uses about 130 MiB of it in small pages.
- The Ir rows of the perf lanes do not show this change. It removes kernel
  time and TLB misses, not instructions.
- Peak memory of Fern stays 1.8 to 4 times that of Koka on these workloads.
  That is the next gap: a record box has 24 bytes of header and one spare
  word.
