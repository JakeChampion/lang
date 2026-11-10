# Lazy argument-target lookup after the Map index

Status: [record] qualified comparison from 2026-11-10.

The baseline is main `da3282d0f3bc2614d45667b17edad8f844eb6a1b`.
The candidate is `597b6e782f5d2825baf3ed07dcf9eeba3dc57be2`.
Both retain the entire upstream `Funcs` Map index from PR #15080.
The candidate avoids unused argument-target searches without a separate argument scan.
The [compressed patch](implementation.patch.gz) preserves its exact code and regression controls.

The fixed input and standard library use `ebb50c78c0ca714180704352881da7e39efb570f`.
Both compilers use qualified producer `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
The [identity record](identities.json) retains source trees, input hashes, and the Go version.
The [source check after the runs](post-run-source-check.json) verifies all three worktrees.

## Algorithm and rejected prepass

Free calls with implicit type arguments retain lookup before argument evaluation.
Other calls resolve at most once when they reach a direct call argument.
The flag also remembers misses.
Closures and the two addressability queries retain their existing behavior.
Dispatch, argument order, and lookup predicates stay unchanged.

The [semantic audit](semantic-audit.json) records the unchanged lookup inputs.
The captured receiver stores its type metadata directly.
The declaration table remains immutable during argument evaluation.
The same-count REPL replacement and earlier session snapshot controls remain active.

The [earlier prepass](../compiler-interpreter-map-shortcut-2026-11-10/README.md) decreases allocations but increases first-group CPU time in four of five pairs.
This candidate removes that extra scan.
The earlier experiment retains every sample and its original source identity.
Its measurements do not describe this candidate.

## Builds and correctness

Normal executables omit debug variables and embedded source files.
They disable profiles and allocation counters and retain ownership certification.
Separate counter executables enable allocation counts.
The [preparation record](prepare.json) retains two successful new builds and two reused baseline executable identities.
The [original build record](reused-baseline-build.json) verifies the reused bytes, source, producer, and flags.

The preparation record also retains the successful pinned source check and read-only format check.
The [targeted validation](validation/validation.json) exits zero.
The [test log](validation/tests.log) contains 388 passing test and subtest results and zero skips.
The Go package completes in 199.295 seconds.
The eleven suites include x86-64, ARM64 under QEMU, WebAssembly, and the upstream 4,000-function cost guard.

## Normal interpreter samples

Five pairs alternate compiler order on the first 32-record group.
Three pairs alternate order on the second group, with 127,126 decoded bytes.
All sixteen normal commands exit zero with identical TAP output within each group.
CPU time is the sum of each child’s user and system CPU time.
The [raw records](subsets/results.json) retain every sample and all outliers.

| Measurement | Main baseline | Lazy candidate |
| --- | ---: | ---: |
| First group median wall time | 3.590483317 s | 3.460304443 s |
| First group median CPU time | 3.584862000 s | 3.441904000 s |
| First group median peak RSS | 140,944 KiB | 148,416 KiB |
| First group wall range | 3.447799179–4.189743923 s | 3.240924484–3.608787231 s |
| Second group median wall time | 6.396827209 s | 5.951670158 s |
| Second group median CPU time | 6.384734000 s | 5.937310000 s |
| Second group median peak RSS | 143,760 KiB | 151,232 KiB |
| Second group wall range | 6.292182685–6.455260764 s | 5.694212862–5.958067070 s |

Paired CPU changes for the first group are -22.3525%, -10.6163%, -0.1367%, +4.4582%, -1.7725%.
Paired CPU changes for the second group are -11.9147%, -7.0077%, -5.1534%.
Negative values indicate less CPU time for the candidate.
The first group has one slower candidate pair.
All three second-group pairs use less CPU time.

Peak RSS increases by 7,472 KiB on both subsets.
The [path control](rss-path-control.json) records different baseline RSS medians for identical compiler bytes at two retained paths.
These samples do not isolate heap costs or establish a specific residency cause.
The higher candidate RSS remains in the report.

## Work counts

| First-group guest instructions | Main baseline | Lazy candidate |
| --- | ---: | ---: |
| Instructions | 17,516,890,468 | 17,364,337,681 |

The reduction is 152,552,787 instructions, or 0.8709%.
The [instruction controls](instructions/results.json) use one sample per normal executable under Valgrind 3.24.0.
Cache simulation, branch simulation, and child tracing are off.
Both runs exit zero with identical TAP output.
These work counts are separate from normal timing samples.

| Allocation events | Main baseline | Lazy candidate | Fewer events |
| --- | ---: | ---: | ---: |
| First group | 29,143,636 | 28,629,458 | 514,178 |
| Second group | 79,975,548 | 66,612,263 | 13,363,285 |

Every counter run frees all allocations and reports zero live bytes.
Each pair produces identical TAP output.
Counter executables are separate from the normal and instruction measurement executables.

## Complete compilation and corpus controls

Both compilers build the complete fixed Opus reference program and its imports to native code.
Three pairs alternate compiler order per cache mode.
Each executable primes its own proof cache.
The separate probes record 112 hits and zero misses.
Actual measurements use normal verifier defaults without the report flag.

| Measurement | Main baseline | Lazy candidate |
| --- | ---: | ---: |
| Cold median wall time | 4.406160149 s | 4.253328599 s |
| Cold median CPU time | 4.401414000 s | 4.249362000 s |
| Cold median peak RSS | 160,464 KiB | 163,408 KiB |
| Cold wall range | 4.225903313–4.665968962 s | 4.157957920–5.220247356 s |
| Warm median wall time | 4.419773623 s | 4.353150268 s |
| Warm median CPU time | 4.417889000 s | 4.348110000 s |
| Warm median peak RSS | 159,952 KiB | 163,024 KiB |
| Warm wall range | 4.221767699–4.421672263 s | 4.242790264–4.407665475 s |

All sixteen compilation commands exit zero and produce identical native output.
Its SHA-256 is `e8ddd643bdc9d72008d930fc42ee8f1574d6f39ae3628891ea0f828f3c67b46f`.
The [compilation records](compilation/results.json) retain every sample and cache probe.
The executable also runs successfully.
These controls establish no general compiler performance gain.

The normal candidate completes all 1,896 records in 130.319683735 seconds with exit zero.
User CPU time is 129.921877 seconds, and system CPU time is 0.367260 seconds.
Peak RSS is 222,912 KiB.
Its complete TAP output matches the native executable output.
The [full control](full/results.json) retains exact identities and child resource measurements.
There is no paired full-corpus baseline timing.

The diagnostic limit remains 1,980 seconds.
The codec, corpus, exclusions, CI package budget, ownership certification, proof rules, negative controls, and tolerances stay unchanged.
Exact-head complete CI remains the merge gate.

## Reproduction and limits

Use isolated worktrees at the recorded revisions and the same qualified producer.
The [preparation script](prepare.py), [measurement script](measure.py), and [compilation script](compile-controls.py) retain complete commands.
Their paths refer to the original worktrees.
Rebuild the baseline with the recorded source, producer, and flags if its retained executable is absent.
Never substitute a different producer without a separate comparison.

The [initial controller](run.sh) completes builds, subset timings, and counter controls.
The [continuation](continue.sh) then completes native compilation measurements.
Targeted validation and instruction controls overlap only after normal timing samples finish.
Full normal interpretation starts after both controls finish.
The [source audit](source-audit.json) records this order.
The shared host limits timing precision.

These subset results establish no external rank or complete-compilation speedup.
Do not add gains from earlier source pairs.
No sample is removed because of its value.

The [manifest](manifest.json) hashes 130 data files.
The README and manifest itself are outside that hash list.
The [compression identities](compression-identities.json) preserve original raw bytes through lossless gzip storage.
Executables and proof caches remain outside this archive.
The ignored original files remain under `build/interpreter-lazy-qualification-20261110-0345` in the primary worktree.

Verify local files with `python3 verify.py`.
Verify committed blobs with `python3 verify.py --commit HEAD`.
