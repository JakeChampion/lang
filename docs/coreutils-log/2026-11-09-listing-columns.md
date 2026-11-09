# Column layout, 2026-11-09

The column algorithm stops each failed column layout before the next name.
It tests column counts from largest to smallest and returns the widest valid layout.
The balanced comparison shows 11.7% to 15.3% less time on the three vertical-column rows.
Across-column results remain mixed. The complete utility goal remains open.

## Selected loss and source

The published November 4 vertical-column ratios are 0.86 for ls, 0.84 for dir, and 0.90 for vdir.
The across-column ratios are 0.88, 0.84, and 0.82.
Fresh baseline results differ from that older source and host. Some baseline rows already beat GNU here.
The full published scoreboard remains unchanged.

The original algorithm visits every column candidate for every name, even after a candidate fails.
The new algorithm accumulates the widths of one candidate and stops when its width exceeds the limit.
It does not assume that different column layouts fit monotonically.

It tests every larger candidate before the selected layout. The original single-column fallback remains.
The width rules, space positions, name escapes, tab stops, sort order, and I/O paths retain their behavior.

The base is main `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
The code commit is `dc9b219c92fd1a56e322ff49cb35cc99ebbbb34c`, with tree `36270c25ce3d5778a1f7d42f2f42b28d20431b3c`.
Both utility columns use the same compiler, SHA256 `741fcd84ac368694fede90eee5e13a9fe172784f90465654cb6f5200cfec202a`.

The pinned bootstrap and smoke checks pass. The compiler, libraries, and benchmark scripts are identical between columns.
Only the column algorithm differs in product source. Later main changes do not inherit these measurements.

## Original workloads

Hyperfine 2.0.0 runs all 18 original workloads for each of ls, dir, and vdir.
Each command has 80 samples after five warmups, in both command orders.
All 25920 primary observations remain in the archive, with every sample and standard deviation.

All 54 workloads match GNU 9.12 stdout, stderr, and exit status before measurement.
The original fixtures, options, operand counts, and direct launch choices remain unchanged.
The fixture tree retains its modes, ownership, names, sizes, and modification timestamps.

No test or compiler build runs beside the measurements.
The initial 960 baseline/GNU pilot samples remain separate.
The host is one shared Linux x86-64 AMD EPYC 9V74 80-Core Processor machine, with the normal scheduler.
No native ARM or Darwin performance claim is made.

Primary vertical-column time reductions are 13.40% / 7.88% for ls and 11.32% / 13.10% for dir.
The vdir reductions are 13.82% / 15.94%.
The across-column reductions are 4.64% / 11.24%, 9.42% / -1.24%, and 13.49% / 12.68%.

The two numbers give forward / reverse order. These observations retain their noise limits.
An unchanged vdir long-format control also moves by 13.9% / 17.1%, so the first comparison alone does not establish attribution.

## Balanced confirmation and controls

The second comparison retains the same original commands and direct launches.
It tests all six command permutations, with 20 samples per command and five warmups.
The workload order rotates between rounds. No sample is discarded.

It covers six column rows and three unchanged long-format controls, for 3240 observations.
The table gives arithmetic means across the six rounds, in milliseconds.
Individual rounds and all standard deviations remain in the archive.

| Workload | Baseline | Candidate | GNU 9.12 | Time reduction |
|---|---:|---:|---:|---:|
| ls -C -w 200 over 4000 names | 3.832 | 3.385 | 4.628 | 11.66% |
| ls -x -w 200 over 4000 names | 3.552 | 3.334 | 4.105 | 6.13% |
| ls -l over 4000 names | 12.453 | 12.933 | 15.450 | -3.85% |
| dir -C -w 200 over 4000 names | 4.117 | 3.486 | 4.197 | 15.31% |
| dir -x -w 200 over 4000 names | 3.827 | 3.446 | 3.735 | 9.95% |
| dir -l over 4000 names | 12.200 | 12.810 | 14.269 | -5.00% |
| vdir -C -w 200 over 4000 names | 3.926 | 3.445 | 4.121 | 12.26% |
| vdir -x -w 200 over 4000 names | 3.796 | 3.297 | 3.749 | 13.13% |
| vdir -l over 4000 names | 12.538 | 12.772 | 14.054 | -1.87% |

All three vertical-column aggregates exceed the 10% threshold. Every vertical-column round favors the candidate, but some individual rounds remain within noise.
The ls across-column result is 6.13%. The dir result is 9.95%, below the threshold.
The vdir across-column aggregate is 13.13%, consistent with its two primary orders.

The long-format aggregates change by -3.85%, -5.00%, and -1.87%, within noise.
These controls do not support a broad long-format improvement.

Several original rows remain noisy, tied, or slower than GNU.
No complete ls, dir, vdir, or full-catalogue win is claimed.
The archive ledger retains all 54 partial observations and their unresolved labels.

## Correctness and platform checks

- GNU probes cover 1008 cases across seven ragged fixtures, both layout directions, narrow and adjacent widths, and three tab sizes.
- Final native GNU 9.12 suites pass 671 cases for each utility, 2013 cases total, plus help/version, with zero skips.
- Final emulated ARM GNU 9.12 suites pass the same 2013 cases and help/version, with zero skips.
- Native and emulated ARM allocation checks each report 631 balanced returns from main per utility, 1893 total, plus 120 usage/error exits.
- Both allocation runs enable the sanitizer. Neither run records an allocation imbalance or a signal exit.
- The new shared corpus adds 84 ragged-row and adjacent-width cases per utility.
- Full source lint, Go vet, Go format, Fern format, and diff checks pass.
- All three full utilities retain byte-identical baseline/candidate WASI E066 capability refusals. No target capability changes.
- Native Darwin qualification remains a hosted CI requirement.

## Failures and scope limits

The initial broad native test filter also selects dircolors, which passes on the native oracle.
The ARM run fails on dircolors patterns for collation and equivalence.
Those failures remain separate from the successful ls, dir, and vdir suites. Their product sources do not share this layout algorithm.
The archive retains that failed run. It does not qualify all ARM coreutils correctness.

The first ARM allocation run omits the explicit GNU path and fails reference discovery.
The corrected run uses the pinned ARM GNU 9.12 directory and an exact utility filter.
The original filter also selects mkdir and rmdir. Their results do not replace an ls check.

The first format command uses an unsupported flag. The supported format check produces an empty diff.
All original failures remain in the archive.

The archive also retains the initial prototype and its checks.
That prototype computes a remainder before the direction branch. The final source computes only the required division or remainder.
No prototype performance result supplies a final comparison.
One exec session expires before its completion read. Independent checks verify its final probe data, source tree, and WASI refusals.

The archive includes a separate prior wc read-result diagnosis at main add0.
Its 640 observations do not supply a measurement column for this source.
It identifies a result-envelope allocation, but its time cost remains unproved.
The earlier zero-fill hypothesis and 128 KiB buffer candidate remain rejected.

The archive also preserves eight post-publication rebuild checks for the prior ownership change.
Those checks belong to the original ownership source. They do not supply a column measurement.

The November 4 ledger still contains 349 ahead, 183 loss, 119 noisy, and 18 rounded-tie rows.
Missing stdbuf coverage and ten unverified SELinux distribution references remain explicit.
The all-utility stop condition remains false.

## Fresh main confirmation

A second pinned bootstrap uses main `b9ba0a1a685d84103abd377440f41217dc3d33b6` plus the same column change.
Its compiler SHA256 is `a3195d8efc590f62bb48ca7a3ba54ab6b9a03216f4a52afada3ddab3c2b1ebc3`. Bootstrap and smoke checks pass.
All six native utility binaries reproduce the exact original baseline/candidate hashes.
The source change outside the column algorithm does not change these native executables.

Fresh GNU 9.12 parity passes 2013 cases on native x86 and 2013 cases on emulated ARM, plus help/version, with zero skips.
The second source also has a separate matched comparison of nine original commands.
It uses 40 samples per command after five warmups, in both orders, for 2160 observations.

All nine commands match GNU stdout, stderr, and status before measurement.
No test or compiler build runs beside these observations.

The means below are milliseconds, forward / reverse. These columns remain separate from the first source cohort.

| Workload | Baseline | Candidate | GNU 9.12 |
|---|---:|---:|---:|
| ls -C -w 200 over 4000 names | 3.843 / 3.807 | 3.467 / 3.113 | 4.517 / 4.252 |
| ls -x -w 200 over 4000 names | 3.601 / 3.568 | 3.206 / 3.515 | 4.245 / 4.440 |
| ls -l over 4000 names | 13.286 / 13.785 | 13.344 / 12.375 | 15.922 / 14.865 |
| dir -C -w 200 over 4000 names | 4.274 / 4.049 | 4.038 / 3.553 | 4.357 / 4.360 |
| dir -x -w 200 over 4000 names | 4.369 / 3.928 | 3.844 / 3.575 | 3.892 / 4.021 |
| dir -l over 4000 names | 14.609 / 14.365 | 13.508 / 12.399 | 15.568 / 14.283 |
| vdir -C -w 200 over 4000 names | 4.156 / 4.329 | 3.717 / 4.428 | 4.341 / 4.267 |
| vdir -x -w 200 over 4000 names | 4.128 / 3.912 | 3.636 / 3.644 | 3.917 / 4.533 |
| vdir -l over 4000 names | 14.232 / 13.155 | 13.467 / 13.387 | 15.174 / 15.219 |

Some individual increments remain within noise or vary by execution order.
These selected rows do not establish full utility acceptance for the new main.
The archive keeps the source identities, both compiler hashes, all binary comparisons, all observations, and both fresh parity logs.

## CPU-affinity control

The fresh unpinned repeat still varies by execution order despite identical executables.
A further control pins the parent and every contender to CPU 0, the first allowed CPU.
The direct launcher, arguments, original fixtures, compiler, and GNU version remain identical for all contenders.
The CPU remains shared. This control does not claim an exclusive host.

The control uses all six command permutations, with 80 samples per command and five warmups.
It adds 12960 observations across six column rows and three unchanged long-format controls.
All earlier unpinned observations remain in the archive.
The table gives arithmetic means across the six rounds, in milliseconds.

| Workload | Baseline | Candidate | GNU 9.12 | Time reduction |
|---|---:|---:|---:|---:|
| ls -C -w 200 over 4000 names | 3.534 | 2.976 | 4.188 | 15.80% |
| ls -x -w 200 over 4000 names | 3.375 | 2.957 | 3.888 | 12.38% |
| ls -l over 4000 names | 12.867 | 12.349 | 15.225 | 4.03% |
| dir -C -w 200 over 4000 names | 4.141 | 3.431 | 4.042 | 17.14% |
| dir -x -w 200 over 4000 names | 3.839 | 3.411 | 3.768 | 11.15% |
| dir -l over 4000 names | 12.118 | 11.447 | 14.260 | 5.53% |
| vdir -C -w 200 over 4000 names | 3.885 | 3.463 | 4.076 | 10.87% |
| vdir -x -w 200 over 4000 names | 3.707 | 3.298 | 3.676 | 11.03% |
| vdir -l over 4000 names | 12.476 | 13.083 | 14.583 | -4.86% |

The six column aggregates show 10.9% to 17.1% less time. The long-format aggregates remain within the 10% threshold.
Some individual rounds still remain within noise. The affinity condition limits the scope of these results.
No full utility or catalogue acceptance follows from these partial rows.

The affinity driver finishes all sample blocks but fails a postcondition after collection.
It compares the precommit patch with an empty working-tree diff after the local source commit.
Independent validation checks the exact committed source patch, all six binary hashes, all sample counts, and every exit status.

All 12960 observations remain intact. The archive retains the original driver, failed log, corrected driver, and independent analysis.
The original wrapper exit status remains 1. No sample was discarded or rerun.

Manual probes also confirm different outputs from the native and ARM GNU 9.12 references on identical dircolors pattern controls.
The configuration or library cause remains unproved. No baseline or reference version changed.

## Evidence

[Raw data and scripts](2026-11-09-listing-columns-data.parts.json), 2211574 bytes.
Archive SHA256: `8ac8e2651c4cc454a1f44ae1e5836200b269cb2dccc8e5a78067af3b3db08497`.
The archive retains all 45240 observations, exact identities, sources, output evidence, failures, controls, and the per-utility ledger.
Executable copies remain in the local evidence directory. The identity file records their hashes and producer.

The upload uses nine lossless archive parts. The manifest records each filename and hash.

Reconstruct the original archive with this command:

```sh
cat docs/coreutils-log/2026-11-09-listing-columns-data.tar.gz.part-* > docs/coreutils-log/2026-11-09-listing-columns-data.tar.gz
```
