# The bump allocator colours each 64 KiB line

2026-11-13. x86-64 and arm64. Follows the comparison in
`../PERCEUS-COMPARISON-2026-11-13.md`.

## The shape

`bench/perceus/fern/run_rbtree.fern` took two times as long as the Koka
program. The instruction counts did not explain it: 721M for Fern and 593M
for Koka at 420,000 inserts. The data-cache misses did: 10.4M for Fern and
0.45M for Koka.

The misses were conflict misses. The bump allocator gave each `Node` a block
of 72 bytes, one after the other.

A program builds a balanced tree from ordered keys. The
nodes of one path are then 2^k nodes apart. The addresses of two such nodes
differ by 72 * 2^k bytes. That is a multiple of 4096 for k >= 9. In a cache
with 64 sets, all those nodes use the same set, and a path has more of them
than the set has ways. Each insert then missed about
20 times on boxes that it read on the insert before.

A cache model with one set showed it. Cachegrind at 420,000 inserts:

| D1 model | D1 misses |
| --- | ---: |
| 32 KiB, 8 ways | 10,401,055 |
| 32 KiB, one set | 1,425,316 |

## The change

When a block crosses a 64 KiB line, the bump allocator moves its pointer
forward by 0 to 960 bytes. The step is 64 bytes times the low four bits of the
number of the 64 KiB line. Blocks of one size are then not a constant distance
apart. The pointer does not pass the end of the heap. The cost is about 0.7%
of the heap.

On x86-64 the test is in `.Lalloc_fast_miss` (`bump_colour` in
`compiler/asm_ir.fern`). It is a 16-bit compare of the end of the block
against its start, and one branch. It adds two instructions to each bump
block. On arm64 the step is in `.Lalloc_nofl`, which `__fern_alloc_bump`
shares, and adds three.

The machine models of the x86-64 allocator follow the step.
`compiler/ssaallocatormachine.fern` runs each way of its two branches, and
`compiler/ssasmallheap.fern` moves its cursor in the same way.

## Measured

x86-64, this container, against main 089d854d.

Cachegrind, D1 of 48 KiB and 12 ways, rbtree at 420,000 inserts:

| | Ir | D1 misses |
| --- | ---: | ---: |
| before | 721,158,833 | 10,401,055 |
| after | 722,421,170 | 1,421,807 |

Wall time, median of five, in milliseconds:

| workload | before | after | Koka |
| --- | ---: | ---: | ---: |
| rbtree 4,200,000 | 1212 | 913 | 599 |
| deriv 10 | 1561 | 1642 | 906 |
| nqueens 13 | 955 | 951 | 802 |

The deriv times are in the noise of this container: eight more pairs of runs
gave 1349 to 1861 before and 1534 to 1742 after. The first form of the step had three instructions for each bump block. With
it, the instruction count of deriv at size 8 went from 104,183,096 to
105,206,075.

rbtree-ck does not change: 6.9M D1 misses before and after at 420,000
inserts, and five pairs of runs at 4,200,000 inserts gave the same times. Its
misses are also misses of the last-level model, so they are not conflicts.

## Traps

- The Ir rows of the perf lanes cannot show this gain. They count
  instructions, and this change adds instructions.
- Cachegrind uses the L3 size for its last-level model. Give it `--D1` and
  `--LL` to model the L1 and the L2 of the host.
- A smaller record box makes the pattern worse, not better. With 64-byte
  blocks the addresses agree modulo 4096 from k = 6.
