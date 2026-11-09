# x86 byte counts, 2026-11-09

The x86-64 byte-count loop counts 128 bytes per iteration.
Four AVX2 comparisons share one loop test.
The original loops handle the final 32 bytes, 16 bytes, and individual bytes.
The change applies to strings and packed byte arrays.
Other targets and unpacked arrays keep their original code.

The isolated scan reduces mean time by 18.0% and 21.5% in opposite execution orders.
The `wc -l` file workload reduces mean time by 6.0% and 7.4%.
Those file differences remain within the published 10% noise threshold.
GNU remains faster on the pipe workload.
This result does not establish a `wc` win across all workloads.

## Method

The host runs Linux x86-64 on an AMD EPYC 9V74 processor.
Both compilers use the same stage0 pin and library sources.
The base source is `aafef0e89a0c35df71b2cbb28ae08b2a524bcd0d`.
The candidate changes only the x86-64 emitter and expands the count tests.
Both compilers build the utility with `-O -target x86-64-linux`.

GNU coreutils 9.12 comes from the official source archive.
Its build lacks optional ACL, xattr, and capability libraries.
The `wc` workloads do not use those libraries.
Hyperfine 2.0.0 records 80 samples per utility and workload in each execution order.

Each command runs five times before measurement.
The locale is C and the time zone is UTC.
No build or test runs during measurement.

The six workloads come from `scripts/coreutils-bench.d/wc.sh`.
GNU `seq 1 8000000` creates the shared input.
The isolated scan counts byte 10 in a 65536-byte array 20000 times.
Each byte contains its index modulo 19.
Both programs verify the same final count.
That scan records 60 samples per implementation in each order.

Each cell below gives forward / reverse results in milliseconds, as mean ± standard deviation.
The forward order is base, candidate, GNU.
The reverse order is GNU, candidate, base.

| Workload | Base | Candidate | GNU 9.12 |
|---|---|---|---|
| lines | 11.976 ± 1.523 / 11.720 ± 1.267 | 11.252 ± 1.450 / 10.847 ± 0.618 | 11.376 ± 1.158 / 11.778 ± 1.755 |
| bytes | 0.313 ± 0.126 / 0.271 ± 0.109 | 0.276 ± 0.153 / 0.154 ± 0.043 | 0.759 ± 0.100 / 0.736 ± 0.086 |
| all | 52.334 ± 3.578 / 52.343 ± 4.251 | 51.060 ± 3.030 / 51.293 ± 3.364 | 79.593 ± 8.154 / 78.904 ± 4.855 |
| words | 50.343 ± 3.103 / 58.745 ± 11.337 | 51.109 ± 4.459 / 53.973 ± 7.114 | 80.131 ± 6.471 / 79.044 ± 11.715 |
| width | 45.086 ± 1.645 / 50.137 ± 2.569 | 47.198 ± 3.813 / 50.615 ± 3.863 | 80.424 ± 10.074 / 91.280 ± 10.770 |
| pipe | 14.306 ± 3.277 / 14.753 ± 1.300 | 13.825 ± 2.351 / 14.693 ± 1.751 | 11.908 ± 2.083 / 12.145 ± 0.803 |
| scan | 27.297 ± 2.800 / 29.268 ± 3.166 | 22.375 ± 2.166 / 22.970 ± 1.632 | — / — |

The byte-only workload does not execute the count loop.
Its variable startup times show the limits of this shared host.
The word-only and width workloads also show variation unrelated to the changed kernel.
No result establishes performance on native ARM64 or Darwin.
The full catalogue still requires fresh measurements.

## Correctness

All six measured workloads match GNU stdout, stderr, and exit status.
All 159 `TestWcParity` cases pass against GNU 9.12.
The 40 byte-input cases and help/version checks also pass.
The expanded byte-count corpus passes through the interpreter, native x86-64, Wasm modules, and WASI components.

The corpus checks vector boundaries, invalid search bytes, retained aliases, and allocation counts.
The native string-count corpus and AVX2 emission test pass.
The source-lint suite, complexity check, and source format checks pass.
These targeted results do not replace full CI.

## Evidence and remaining work

The [evidence archive](2026-11-09-count-byte-data.tar.gz) contains all raw samples, commands, hashes, and test logs.
It contains the source patch, source inventory, and a ledger for all 106 published utilities.
That ledger marks 183 rows as losses and 137 rows as inconclusive under the rounded 10% rule.
It records the absent `stdbuf` utility separately.
The ledger describes the published November 4 run, not fresh full-catalogue qualification.

The archive retains two benchmark-driver failures and the malformed pipe batch.
The first reader expected the older Hyperfine JSON schema.
The second driver used the older shell-option syntax.
The corrected driver preserves valid completed samples and measures the pipe workloads with explicit Bash and pipe failure checks.

It verifies command names, sample counts, and exit statuses.
The completed comparison contains 3120 eligible samples.
The malformed batch and preliminary baseline batch remain separate.

The pipe workload remains a measured loss to GNU.
The read path allocates each chunk and copies short reads into another array.
That path needs separate measurement before a further implementation change.
The scheduled task remains active until every utility meets the complete acceptance condition.
