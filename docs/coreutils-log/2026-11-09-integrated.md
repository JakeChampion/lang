# Integrated wc and tail qualification, 2026-11-09

The integrated source retains a large advantage on the four-million-line tail workload.
It does not establish a complete utility win.
The line-count wc workloads retain losses or inconclusive results.
The byte-tail workload remains inconclusive in one execution order.

## Source and method

PR #14885 merged at `db95ecd63ede29cde54fac26276e462ec6ac39f1`.
PR #14900 merged at `d283621bf71c5ada54c41596dfa95fd2ba567d8a`.
This comparison uses the integrated compiler, libraries, and utilities from main `180c8416120d3a2bc27102ab351748b416014967`.
The test repair source is `4bd675548185d4df95bcb7573a482c16a9d9c179`.
That repair does not change the compiler, libraries, or utilities.

Other compiler changes also entered main. These measurements do not isolate either performance PR.
The earlier reports retain their original source and binary identities.
An earlier integrated build from `30dc3b205b10ded4ee6b951be8a52c1bfa340877` is also preserved.
It supplies no measurement column in this report.

The pinned stage0 builds the new compiler through `bootstrap/bootstrap.sh build`.
The compiler and smoke checks pass.
The compiler SHA256 is `b7d1841c9aa6b19826816030a5e267fd56b7b9815b2afb40f26aa355d73bae57`.
The source inventory and all utility and reference hashes are in the archive.

All eleven published wc and tail workloads run against GNU coreutils 9.12.
The fixture contains 62888896 bytes from the original eight-million-line input.
Each command supplies 80 samples after five warmups, in each of two opposite execution orders.
The comparison contains 3520 eligible samples with successful exit status.
Every workload matches GNU stdout, stderr, and exit status before measurement.
No test or compiler build runs alongside the performance samples.

## Matched results

Times are means. The raw samples retain standard deviations and individual durations.
Ratios within about 10% of one remain inconclusive.

| Workload | Fern forward / reverse ms | GNU forward / reverse ms | GNU / Fern ratios |
|---|---:|---:|---:|
| wc -l of a 62 MiB file | 12.293 / 16.295 | 11.556 / 14.390 | 0.940 / 0.883 |
| wc -c of a 62 MiB file | 0.173 / 0.376 | 0.818 / 1.034 | 4.732 / 2.748 |
| wc of a 62 MiB file | 53.092 / 55.189 | 83.228 / 85.083 | 1.568 / 1.542 |
| wc -w of a 62 MiB file | 50.193 / 63.055 | 81.815 / 84.595 | 1.630 / 1.342 |
| wc -L of a 62 MiB file | 45.740 / 57.514 | 81.295 / 95.470 | 1.777 / 1.660 |
| wc -l from a pipe | 13.335 / 17.591 | 12.333 / 12.442 | 0.925 / 0.707 |
| tail -n 10 of a 62 MiB file | 0.200 / 0.340 | 0.879 / 0.937 | 4.396 / 2.754 |
| tail -n 4000000 of a 62 MiB file | 13.907 / 14.002 | 35.148 / 48.199 | 2.527 / 3.442 |
| tail -c 32M of a 62 MiB file | 8.609 / 7.715 | 8.952 / 10.856 | 1.040 / 1.407 |
| tail -n 10 from a pipe | 22.766 / 23.749 | 95.092 / 92.167 | 4.177 / 3.881 |
| tail -n +4000000 of a 62 MiB file | 14.660 / 13.574 | 37.567 / 39.498 | 2.563 / 2.910 |

Execution order changes several results. The archive retains both orders without exclusions.
The partial ledger keeps each observation separate from the older full-catalogue classifications.

## Correctness and diagnostic checks

The selected wc, tail, and stat parity suites pass on native x86-64 and emulated ARM64.
Neither run skips a case.
The wc checks include 159 ordinary cases, 40 byte cases, and 40 real-GNU command-prefix regression cases.
The tail checks include 226 ordinary cases and the shared head/tail byte corpus.
The stat checks include 764 ordinary cases, 61 byte cases, and the three file-context assertions.
Help and version checks also pass.

The sanitized wc, head/tail, and stat byte suites pass with the integrated compiler.
They exercise native x86-64, ARM64 through QEMU, and the applicable WASI paths.
The stat WASI check verifies the expected capability refusal.
The allocation census checks remain enabled.

Separate strace diagnostics record 961 wc reads for both the regular file and the pipe.
Each trace has 959 complete 65536-byte reads, one 39872-byte read, and one EOF read.
The tracer perturbs the scheduler. These counts are not untraced performance measurements.
They do not establish frequent short reads as the cause of the pipe loss.

## Evidence and limits

The [raw archive](2026-11-09-integrated-data.tar.gz) contains samples, commands, probes, hashes, source inventories, build logs, and test logs.
The comparison uses one shared AMD EPYC Linux x86-64 host with the normal scheduler.
ARM64 results establish correctness under emulation, not native speed.
Native Darwin execution remains unverified here.

The published scoreboard remains the November 4 full run.
This partial comparison does not replace it.
The ledger retains the missing `stdbuf` utility and the distribution SELinux reference eligibility gaps.
The all-utility stop condition remains false.
