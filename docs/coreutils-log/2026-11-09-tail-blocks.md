# Tail block counts, 2026-11-09

The regular-file line search counts delimiters across a complete block first.
It skips that block when the requested count still covers all its delimiters.
Only the block with the output boundary needs an individual delimiter search.
The final partial record keeps its separate count.

The four-million-line workload takes 10.898 and 11.668 ms in opposite execution orders.
GNU coreutils 9.12 takes 34.090 and 34.682 ms.
These results give 3.13x and 2.97x GNU performance on that workload.
The last-byte workload remains inconclusive in one order under the 10% noise rule.
This change does not establish a complete utility win.

## Method and results

The base source is `8507ad3b563d6cdb8476fc95ce82d68367efd1dc`.
Both utilities use the same compiler and library sources.
The compiler hash is `c8ae49e18e8cc0e3e6507138a093c007c1bee90aef718b3271dd748cd17a2c92`.
The compiler sources match the base through the previous site-only main changes.
The open x86 count-kernel PR is absent from both candidates.

The host uses an AMD EPYC 9V74 processor and Linux x86-64.
The build flags are `-O -target x86-64-linux`.
The oracle is GNU coreutils 9.12 from the official source archive.
Hyperfine 2.0.0 records 80 samples per workload and implementation in each order.
Each command runs five times before measurement.
No build or test runs during measurement.

The five workloads come from `scripts/coreutils-bench.d/tail.sh`.
GNU `seq 1 8000000` creates the shared input.
The locale is C and the time zone is UTC.
File commands execute directly with standard output on `/dev/null`.
The pipe workload uses Bash with pipe failure checks and the same GNU `cat` for all implementations.

Each cell gives forward / reverse results in milliseconds, as mean ± standard deviation.
The forward order is base, candidate, GNU.
The reverse order is GNU, candidate, base.

| Workload | Base | Candidate | GNU 9.12 |
|---|---|---|---|
| last ten lines | 0.164 ± 0.052 / 0.168 ± 0.062 | 0.171 ± 0.076 / 0.172 ± 0.058 | 0.755 ± 0.277 / 0.656 ± 0.083 |
| last four million lines | 33.060 ± 2.211 / 33.309 ± 3.183 | 10.898 ± 0.880 / 11.668 ± 1.391 | 34.090 ± 3.239 / 34.682 ± 2.583 |
| last 32 MiB | 5.908 ± 0.583 / 6.909 ± 1.086 | 5.855 ± 0.411 / 7.209 ± 1.148 | 7.523 ± 1.158 / 7.787 ± 2.009 |
| last ten lines from pipe | 21.696 ± 2.294 / 23.268 ± 2.274 | 21.950 ± 2.577 / 24.505 ± 2.203 | 83.523 ± 5.282 / 93.414 ± 5.162 |
| from line four million | 12.587 ± 1.293 / 13.221 ± 1.118 | 12.925 ± 2.815 / 13.943 ± 2.068 | 32.611 ± 1.613 / 36.365 ± 2.260 |

The other workloads do not establish an improvement over the base.
Their differences remain inside the published noise threshold.
The shared host and command order still affect the small differences.
No result establishes native ARM64 or Darwin performance.

## Correctness

All five measured workloads match GNU stdout, stderr, and exit status.
The comparison contains 2400 eligible samples.
The native suite passes all 226 ordinary `tail` oracle cases and 39 byte-input cases.
Those cases include follow behavior, read errors, and output errors.
Help and version checks also pass.

The shared corpus adds exact block counts, adjacent counts, NUL records, unfinished records, and blocks without delimiters.
Its 38 cases pass on sanitized x86-64 and WASI with balanced allocation counts.
The source format checks pass.
The local environment lacks ARM64 emulation tools and a native Darwin host.
Full CI still needs its normal platform gates.

## Evidence and rejected experiments

The [evidence archive](2026-11-09-tail-blocks-data.tar.gz) contains raw samples, scripts, hashes, tests, and the source patch.
It also contains the catalogue ledger and prior prototype results.
The ledger keeps the published November 4 results separate from this candidate.
The absent `stdbuf` utility remains an explicit gap.

A separate copy-buffer pilot did not establish a repeatable gain.
The candidate therefore keeps the original read and write sizes.
The archive preserves the combined prototype and its tests.
The final binary matches the earlier block-count-only binary exactly.

A separate `wc` buffer candidate also failed performance qualification.
Its 2880 samples show only 4.0% and 4.7% pipe improvements.
Its native and WASI correctness checks pass, but these measurement differences remain within noise.
The rejected source, tests, and raw data remain in the local experiment worktree.

This candidate builds from main without the open count-kernel PR.
Each independent pull request requires its normal CI and merge.
The subsequent integrated source needs fresh checks and measurements.
The scheduled task remains active until the complete catalogue meets its acceptance condition.
