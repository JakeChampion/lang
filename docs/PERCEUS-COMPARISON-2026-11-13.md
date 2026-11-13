# Standard Perceus workloads: Fern, Koka and Lean, 2026-11-13

Status: [record] measurement snapshot of Fern against Koka and Lean on the standard Perceus workloads, after the work of #15610.

This snapshot repeats [the snapshot of 2026-10-08](PERCEUS-COMPARISON.md)
with the same harness, sizes and toolchain versions. The machine is not the
same: these runs are on an x86-64 cloud container, and those were on a native
ARM Linux VM. Compare the ratios of the two snapshots, not the times.

Fern is second on the ordinary tree and on n-queens. Fern is third on the
checkpoint tree, differentiation and constant folding. On the ordinary tree
Fern took 7.6 times the time of Koka in October. It now takes 2.0 times.

Wall time includes process startup, computation, answer printing and teardown.
Each cell is median milliseconds, followed by the observed minimum and maximum.

| Workload | Size | Fern | Koka | Lean |
| --- | ---: | ---: | ---: | ---: |
| rbtree | 4,200,000 | 1273.17 (1156.09-1326.20) | 622.23 (596.85-647.80) | 1772.22 (1703.30-1784.95) |
| rbtree-ck | 4,200,000; retain every fifth tree | 3007.13 (2690.53-3270.19) | 1667.37 (1627.17-1824.32) | 2836.26 (2733.15-2874.17) |
| deriv | 10 derivatives | 1502.61 (1327.76-1592.35) | 850.99 (831.60-944.88) | 1121.63 (1097.73-1210.80) |
| nqueens | 13 | 874.09 (869.00-988.92) | 784.92 (776.77-818.37) | 1182.99 (1129.58-1248.95) |
| cfold | depth 20 | 281.47 (259.82-318.41) | 182.92 (166.64-216.79) | 236.95 (205.98-276.70) |

The ranges of Fern and Lean overlap on the checkpoint tree and on constant
folding.

Fern time as a multiple of the Koka time and of the Lean time, from the
medians of each snapshot:

| Workload | Koka, 2026-10-08 | Koka, 2026-11-13 | Lean, 2026-10-08 | Lean, 2026-11-13 |
| --- | ---: | ---: | ---: | ---: |
| rbtree | 7.57 | 2.05 | 2.27 | 0.72 |
| rbtree-ck | 4.90 | 1.80 | 2.33 | 1.06 |
| deriv | 1.33 | 1.77 | 1.37 | 1.34 |
| nqueens | 1.40 | 1.11 | 0.92 | 0.74 |
| cfold | 1.59 | 1.54 | 1.05 | 1.19 |

The deriv and cfold ratios did not fall. The two machines differ in ISA,
cache and memory. This table cannot show whether Fern lost ground on those
two workloads, or whether the machines weigh them differently.

Peak resident memory is the median of five whole-process peaks, in MiB.
Fern allocation counts come from a separate instrumented run of the same
workload. Every allocation census had equal frees and zero live bytes at exit.

| Workload | Fern MiB | Koka MiB | Lean MiB | Fern allocations | Fern allocations, 2026-10-08 |
| --- | ---: | ---: | ---: | ---: | ---: |
| rbtree | 288.38 | 133.07 | 200.53 | 4,200,004 | 136,026,039 |
| rbtree-ck | 2057.00 | 1151.17 | 1382.70 | 30,237,779 | 136,026,039 |
| deriv | 850.00 | 459.12 | 552.62 | 25,916,348 | 38,843,655 |
| nqueens | 185.62 | 99.04 | 132.54 | 9,349,782 | 9,349,784 |
| cfold | 209.50 | 157.05 | 166.61 | 2,977,771 | 3,858,390 |

Fern has the highest peak on each workload, between 1.3 and 2.2 times the
peak of Koka.

## The checkpoint tree kept no tree

The first run of this snapshot gave Fern 1156 ms on the checkpoint tree, ahead
of Koka. That run did not do the work of the workload. Its census had
4,200,004 allocations, one for each insert, and its peak was 288 MiB.

The driver printed the count of the newest tree only. Nothing read the list
of the older trees, and the compiler removed the list. Each insert then
found a unique tree and changed it in place. The program was correct: a
variant that sums each tree of the list gives the answer of the two
interpreters.

The Fern driver and `bench/perceus_rbtree_ck.fern` now read the length of the
list and compare it with the expected count. The compiler must then keep
each tree. The table above is from that driver. The walk of 840,001 list
cells is small against 4,200,000 inserts.

The compiler of 2026-11-13 morning removed the list too, so the cause is
older than #15610. The CI rows `perceus_rbtree_ck.ir` measured the ordinary
tree with 64-bit keys for an unknown time.

## Method and scope

Measurements were taken on 2026-11-13 in an x86-64 Linux cloud container. The
guest reports Linux 6.18.44, 4 logical CPUs and 16,867,405,824 bytes of
memory, with no cgroup CPU or memory cap. The host is shared, and other work
can contend with a run. These are measurements on this environment, not
universal rankings.

The harness gives each process an unlimited Linux stack, as in October. One
verified warm-up for each language and workload is not in the results. The
execution order rotates between repeats. All 75 measured answers and the five
Fern censuses passed the independent result checks.

Fern uses the primary compiler built from
`31ec74cfd2054cf0ea2a1e861123a2b8971dae88`, with the driver change of this
snapshot in the working tree. Koka is 3.2.9 and Lean is 4.34.1, commit
`5045d0056413266e57c625dcd7c365b10e377c52`, the versions of October. The
SHA-256 values of the x86-64 Linux release archives are:

- Koka: `310459831a7c6fa6a0cd8e0e02d6b5b3b36441d5e7ce24168bda016cd2e95eee`.
- Lean: `47bf4bbd78f70c2e9670598ab7124d92b6efb7330ff33e5fbb4030f6fd72e4e4`.

Koka uses `-O2` with the GCC of the container. Lean emits C and uses
`leanc -O3` with its bundled clang. Fern uses normal native compilation.
[builds.json](benchmarks/perceus-2026-11-13/builds.json) has the build
commands and the binary hashes.
[results.json](benchmarks/perceus-2026-11-13/results.json) has each run, and
[metadata.json](benchmarks/perceus-2026-11-13/metadata.json) has the source
hashes and the environment.

## What changed in Fern

`docs/ssa-log/` and `docs/rc-log/` have one entry for each change of
2026-11-12 and 2026-11-13. On x86-64, the ordinary tree at 100,000 inserts
fell from 271,064,146 instructions to 154,026,828 in those two days.

## Reproduce

The command of [the October snapshot](PERCEUS-COMPARISON.md) applies, with
the x86-64 release archives of the two toolchains.
