# The run before a run decides its system time

2026-11-15. The benchmark harness, not the compiler.

## The shape

`scripts/bench-perceus.py` timed the five workloads at full size, with
Fern, Koka, Lean and Roc and 8 repeats. Fern was behind Koka on
rbtree-ck by 18% at the median. Run alone, the same Fern binary was
ahead of Koka.

The system time of Fern was 640 ms to 840 ms in seven of the eight
runs, and 164 ms in one. Koka had 130 ms to 220 ms, except in the runs
directly after Roc.

## The cause

The Fern heap asks for huge pages (`madvise` with `MADV_HUGEPAGE`).
The kernel of the container has `defrag` set to `madvise`. Thus a
fault in that heap compacts memory before the kernel can give a huge
page. When free memory is in small fragments, the compaction takes a
long time.

A run leaves the free memory in the state that it used it. Roc frees
1.5 GB that it took in 4 KiB pages. Fern frees 1.15 GB of huge pages.

The harness rotated one fixed order, `fern, koka, lean, roc`. A
rotation keeps each neighbour, so Fern always ran after Roc or after
Lean, and Koka always ran after Fern.

The table gives the system time of rbtree-ck 4,200,000 in milliseconds. The
first column is a run directly after a Roc run. The second is the next run.

| | after Roc | next |
|---|---|---|
| Fern | 787, 1812, 2276 | 140, 143, 181 |
| Fern without the huge-page advice | 552, 599, 526 | 651, 627, 541 |
| Koka | 2883, 3037, 1838 | 194, 308, 112 |

The advice is correct for Fern on free memory. Without it, the next
run takes 4 times the system time and has 294,000 faults in place of
1,600. Koka has the same cost after Roc.

## The change

`turn_order` gives row `repeat` of a balanced Latin square. Each
language takes each position equally often. Each language runs directly
after each other language equally often. Four languages have four rows,
and three languages have six.

## Measured

The same binaries at full size, 8 repeats, x86-64. Each cell is the
median processor time, with the best run in brackets, in milliseconds:

| workload | Fern | Koka | Lean | Roc |
|---|---|---|---|---|
| rbtree | 1033 (847) | 918 (840) | 3833 (3412) | 4840 (4628) |
| rbtree-ck | 2422 (2074) | 2677 (2043) | 5560 (5133) | 8502 (7810) |
| deriv | 1282 (1070) | 1398 (1220) | 2264 (1548) | 5394 (5125) |
| cfold | 196 (147) | 243 (236) | 405 (387) | 538 (513) |
| nqueens | 1105 (1043) | 1091 (1013) | 1783 (1641) | 5198 (5002) |

The peak resident memory in MB:

| workload | Fern | Koka | Lean | Roc |
|---|---|---|---|---|
| rbtree | 130 | 133 | 201 | 215 |
| rbtree-ck | 1150 | 1151 | 1383 | 1523 |
| deriv | 420 | 459 | 553 | 1080 |
| cfold | 136 | 157 | 167 | 134 |
| nqueens | 94 | 99 | 133 | 134 |

The user time of rbtree is level: about 880 ms for each of Fern and
Koka. The median of Fern is higher because of four runs with a high
system time, of which two come after Roc.

At the small sizes, Fern runs fewer instructions than Koka on each
workload:

| workload | Fern | Koka |
|---|---|---|
| rbtree 420,000 | 582 M | 593 M |
| rbtree-ck 420,000 | 877 M | 1079 M |
| deriv 8 | 103 M | 130 M |
| cfold 16 | 33.4 M | 42.6 M |
| nqueens 10 | 23.9 M | 24.6 M |

## Not done

A cleaner measure compacts memory before each run, or runs each
language on its own host. The first needs root in the container.
