# Receiver union membership experiment

Status: [rejected], 2026-11-10.

The trial reuses the existing union index for receiver membership queries.
The fixed current-source comparison increases cold checker CPU time in seven of eight pairs.
Its CPU median increases by 10.7%.
The other workload gains do not justify this unresolved compilation cost.
The final source removes the optimization and retains the regression tests and kernel fixture repair.
No compiler speedup or external rank remains in the final change.

The [summary](summary.json) separates every source comparison.
The [manifest](manifest.json) records 773 data hashes.
The [compression identities](compression-identities.json) record 84 lossless raw identities.
The [source map](source-files.json) records each original path, raw byte count, and hash.

| Phase | Baseline compiler | Candidate compiler | Fixed input and standard library |
| --- | --- | --- | --- |
| Initial | `0e715dff` | `99fda7a6` | `0e715dff` |
| Intermediate | `515ddee8` | `3d51e188` | `515ddee8` |
| Rejected final trial | `b442b6b9` | `67b21637` | `b442b6b9` |
| Retained correctness | `e4494347` | `c02d2b4c` | `e4494347` |

These source totals are not pooled.
The compiler and standard library in the retained source exactly match main `e4494347`.
Fresh main and retained normal compiler images match byte for byte.
Their SHA-256 is `68e415467a0d3b40323959c689318c918d9a2adc8415d58e834dde17de914a83`.
This equality adds no performance claim.

The trial changes only two receiver-query call sites and one private helper.
The helper uses the existing `UnionTable.index.find` and exact raw variant names.
It retains the first duplicate declaration, including an immediate false result when that declaration lacks the variant.
The recursive array-based membership API remains unchanged throughout the trial.
No new index, graph cache, unsafe operation, or proof shortcut appears.

All comparison compiler images use the same qualified producer from source `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
The producer SHA-256 is `a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751`.
Normal measurement builds use no `-g`, embedded sources, CPU profile, or allocation counters.
Certification remains enabled.
Cold runs disable only the proof memo.
Warm runs use separate retained caches for each compiler and workload.

Each source comparison has eight fixed balanced pairs per workload and proof-cache mode.
Four pairs use each compiler order.
The workloads are complete native Opus and checker compilations, including imports, assembly, and linking.
Equal-length compiler paths and per-run binary hashes protect the paired inputs.
No unfavorable sample triggers a retry or a larger sample count.

The rejected final trial uses 64 timed commands and eight separate cache primes and probes.
Actual timed runs use normal verifier defaults and omit the report flag.
All four separate report probes show cache hits and zero misses.
Twelve separate counter commands follow the normal timings.
All 84 commands exit zero, and every emitted executable matches within its workload.
The resulting Opus program passes its full reference output control.

| Rejected workload | Base CPU median, s | Trial CPU median, s | Base wall median, s | Trial wall median, s | Base / trial median RSS, KiB |
| --- | ---: | ---: | ---: | ---: | ---: |
| opus cold | 4.264506 | 4.060956 | 4.266168 | 4.062600 | 167572 / 167572 |
| opus warm | 4.154529 | 4.013081 | 4.155812 | 4.014467 | 167060 / 167060 |
| checker cold | 11.850931 | 13.118910 | 11.852175 | 13.123284 | 579476 / 579476 |
| checker warm | 10.156989 | 9.806840 | 10.157874 | 9.808129 | 576916 / 576916 |

The cold checker paired CPU changes are `+6.9975`, `+16.4557`, `+8.0840`, `+2.5985`, `+17.8246`, `+12.6481`, `+7.3994`, and `-1.2075` percent.
The other three combinations have lower medians, with slower individual pairs and substantial variation.
Every sample, range, order, CPU value, wall value, and RSS value remains in the raw records.
The physical cause of the timing difference remains unknown.

Allocation counts remain equal between compilers in every cold and warm comparison.
Every counter run has nonzero allocations, balanced frees, and zero live bytes.
Counter compilers contain allocation instrumentation, but their executions retain normal output-generation flags.
These counter runs do not supply production timing samples.
The normal full interpreter control is not a leak-check run.

The instruction matrix starts after the normal sweep and then stops after rejection.
Its first candidate run receives SIGTERM before completion.
The failed result and partial trace remain intact.
This phase supplies no completed instruction pair or instruction reduction.
No additional timing sample seeks a favorable outcome.

The intermediate comparison also retains all 84 successful commands.
Its four CPU medians are lower, with mixed paired changes, equal allocation counts, and equal median RSS.
Interactive Git and filesystem work overlaps parts of that intermediate sweep.
Its separate concurrency note preserves that limitation and the unknown operation intervals.
These earlier results do not override the later cold checker regression.

The initial comparison records 16,923,232,536 versus 16,150,097,649 guest instructions for one cold Opus pair.
That 4.57% reduction belongs only to sources `0e715dff` and `99fda7a6`.
The initial baseline profile records a separate unpaired count and a qualified offline symbol map.
The map reproduces that original normal image byte for byte, and the reconstructed image never executes for measurement.
Its addresses and percentages do not qualify any later compiler image.

The original baseline copy lacks execute permission and exits 126 before compilation.
The corrected controller preserves the successful candidate run and collects only the missing baseline half.
Its executable bytes remain unchanged.
The archive also retains the temporary-directory ENOSPC failures and all zero-execution waiter corrections.
No failed or unfinished collector is called a passing measurement.

CI also exposes a kernel scheduler fixture that does not guarantee a visit to both cores for every process.
Six ordinary main repetitions pass locally, so no natural local reproduction is claimed.
A private one-second timer control fails the original migration and concurrency assertions.
The corrected dispatch policy passes 12 standard scheduler runs and every original assertion under that same private timer.
The product timer, worker computation, assertions, and budgets remain unchanged.
This control does not prove a unique cause for the CI timing difference.

The retained source passes 20 targeted suites with 150 test and subtest passes and zero skips.
Its Go parser and checker units report 2,864 passes and zero skips.
All 16 Paper runner controls pass, without an Android device claim.
Pinned source checks, normal own-source checks, format checks, Go vet, and complete CI self-tests pass.
Current native Opus compilation and execution and the full normal interpreter pass with identical reference TAP output.
The original 1980-second diagnostic limit remains unchanged.

The normal timing host is shared, without exclusive CPU or frequency control.
Small CI requests, task metadata updates, and publication helper edits overlap parts of the final trial sweep.
No other local build, test, or profile workload overlaps those timing samples.
The frozen source trees, compiler binaries, and fixed inputs remain unchanged.
No outlier is discarded or attributed to a physical cause.

The archive retains commands, environments, source identities, scripts, raw output, negative controls, and both removal patches.
Large or whitespace-sensitive records use lossless gzip.
Raw Go experiment sources also use gzip so they do not become Go packages in the repository.
Compiler executables and proof caches remain in the preserved local worktrees, with recorded identities.

Run the committed-data verifier from the repository:

```sh
python3 docs/benchmarks/compiler-receiver-union-index-2026-11-10/verify.py --commit HEAD
```

The verifier checks the selected committed archive and its historical source identities.
It does not claim that a later main compiler has the same performance.
The retained scripts contain the exact producer, build, test, cache, and comparison commands.

For workload reproduction, create detached checkouts for the exact source pair in the summary.
Use the [bootstrap process](../../BOOTSTRAP.md) from that baseline checkout to obtain its pinned producer.
Verify the recorded producer hash before a build.
Use copies of the retained scripts when workspace paths differ.
Preserve the original commands and record each path change.
Do not overwrite the historical raw records.
