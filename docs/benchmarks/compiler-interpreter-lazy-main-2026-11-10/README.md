# Lazy argument-target lookup after current main

Status: [record] matched comparison from 2026-11-10.

The baseline is main `c9547605f513b81cb1c26d1cb4deaf37b20648bd`.
The candidate is `c05b5ce874fc4a6ec6b0be45ee71d3ed1bd5f8f1`.
Main contains the Paper Back, resumed TLS expectation, nested-owned-field checker, and CI worker repairs.
This comparison does not reuse the older main totals.
The [compressed patch](implementation.patch.gz) retains the exact product change and regression controls.

Both compilers retain the upstream `Funcs` Map index and its 4,000-function cost guard.
This change only defers unused argument-target lookup.
Free calls with implicit type arguments retain eager lookup.
Other calls resolve once before the first direct call argument needs the result.
The flag remembers both success and a miss.
Closures and both addressability queries retain their prior behavior.

The interpreter source matches the earlier qualified lazy implementation byte for byte.
The [earlier semantic audit](original-lazy-semantic-audit.json) describes its immutable lookup inputs.
The [source audit after collection](post-run-source-check.json) verifies this identity and the frozen worktrees.
The complete compiler and standard library changed, so this archive retains a separate matched comparison.

## Inputs and builds

The fixed input and standard library use `ebb50c78c0ca714180704352881da7e39efb570f`.
Both compilers use qualified producer `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
Its SHA-256 is `a66fcf94f418bea5f969bfaf2b07e6fe56235d2565ca405c950e6e263187a751`.
The [preparation record](prepare.json) retains four successful fresh builds and all executable identities.

Normal executables omit debug variables and embedded source files.
Profiles and allocation counters are off, with ownership certification on.
Separate counter executables enable allocation counts.
The proof cache is off for interpretation and cold compilation.
Each compiler uses its own primed cache for warm compilation.

## Correctness and integration

Pinned source check, read-only format check, Go vet, and CI self-tests exit zero.
Twelve targeted suites report 413 passing test and subtest results, with zero skips.
The package completes in 266.241 seconds.
The [validation records](validation/validation.json) retain the exact selectors and commands.

The suites cover x86-64, ARM64 under QEMU, WebAssembly, and the function-count guard.
They retain the nested-owned-field positive and negative controls.
The same-count REPL regression preserves replacement, declaration order, and earlier session snapshots.

Both TLS selectors that failed the superseded head also pass after the main integration.
Their [separate control](integration/tls-control-result.json) reports 14 test and subtest passes and zero skips.
These tests start after the original performance sequence finishes.
Main supplies the expectation repair for the retained client certificate.
The archive retains the original failed CI logs and the baseline conflict resolution.
Every numerical baseline row and tolerance matches the newer main at integration.

## Normal interpreter samples

Five pairs alternate compiler order on the first 32-record group.
Three pairs alternate order on the second 32-record group, with 127,126 decoded bytes.
All sixteen normal commands exit zero with identical TAP output within each group.
CPU time is each child's user time plus system time.
The [raw records](subsets/results.json) retain every sample and outlier.

| Measurement | Main baseline | Lazy candidate |
| --- | ---: | ---: |
| First group median wall time | 3.954127808 s | 3.888582325 s |
| First group median CPU time | 3.937680000 s | 3.885495000 s |
| First group median peak RSS | 148,420 KiB | 148,420 KiB |
| First group wall range | 3.805893840–4.083626059 s | 3.832552754–4.288919770 s |
| Second group median wall time | 7.344728302 s | 6.963385927 s |
| Second group median CPU time | 7.335690000 s | 6.950353000 s |
| Second group median peak RSS | 151,236 KiB | 151,236 KiB |
| Second group wall range | 6.691446286–7.418480127 s | 6.817709065–7.816885909 s |

Paired CPU changes for the first group are +10.7486%, +0.1233%, -3.2234%, -2.6071%, +1.2843%.
Paired CPU changes for the second group are +16.8395%, -5.2529%, -7.9367%.
Negative values indicate less CPU time for the candidate.
Both groups contain slower candidate samples.
These shared-host timings establish no consistent speedup.
The report retains all memory measurements without a heap or residency attribution.

## Allocation events and guest instructions

| Allocation events | Main baseline | Lazy candidate | Fewer events |
| --- | ---: | ---: | ---: |
| First group | 29,146,366 | 28,632,188 | 514,178 |
| Second group | 79,978,278 | 66,614,993 | 13,363,285 |

Every counter run frees all allocations and reports zero live bytes.
Each pair produces identical TAP output.
Counter executables are separate from production timing executables.

One Valgrind 3.24.0 sample per normal executable measures the first group.
Guest instructions are 17,517,700,658 for main and 17,365,147,731 for the candidate.
The difference is 152,552,927, or 0.8709% fewer instructions.
The [instruction record](instructions/results.json) retains commands, raw counts, output hashes, and tool identities.

Cache simulation, branch simulation, and child tracing are off.
Both runs exit zero with identical TAP output.
No timing claim uses instrumented runs.

## Complete compilation and full corpus

Both compilers build the complete fixed Opus reference program and its imports to native code.
Three pairs alternate compiler order per cache mode.
Separate probes verify 112 hits and zero misses for each compiler's own cache.
Actual measurements use normal verifier defaults without the report flag.

| Measurement | Main baseline | Lazy candidate |
| --- | ---: | ---: |
| Cold median wall time | 4.620164066 s | 5.111032820 s |
| Cold median CPU time | 4.615265000 s | 5.108573000 s |
| Cold median peak RSS | 163,408 KiB | 163,408 KiB |
| Cold wall range | 4.404115264–4.983414701 s | 4.449394432–5.305480809 s |
| Warm median wall time | 4.317935220 s | 4.517029757 s |
| Warm median CPU time | 4.313873000 s | 4.515151000 s |
| Warm median peak RSS | 163,024 KiB | 163,024 KiB |
| Warm wall range | 4.307507506–4.519453084 s | 4.469027077–4.520969962 s |

All sixteen compilation commands exit zero and produce identical native output.
The [compilation records](compilation/results.json) retain every sample and cache probe.
Both cold and warm candidate medians are slower in this comparison.
Paired cold CPU changes are +2.5226%, +14.9311%, +0.9184%.
Paired warm CPU changes are -1.1179%, +4.9229%, +4.6948%.

All three cold pairs and two warm pairs use more candidate CPU time.
These controls establish no general compiler performance gain.

All three slower cold CPU pairs prompted a separate check of complete-compilation work.
One normal-binary instruction sample per compiler records 18,663,497,232 guest instructions for main and 18,663,497,396 for the candidate.
The [separate instruction record](compilation-instructions-v2/results.json) retains both raw counts and identical native output hashes.
Its candidate-minus-main difference is +164 instructions.
This diagnostic pair follows every normal timing and TLS control.
It does not establish a timing cause or a general compiler performance result.

A tool-version preflight detected the missing path for the private Valgrind libraries before either compiler measurement started.
The [preflight record](compiler-work-preflight.json) retains that failure and the terminated waiter with zero measurement samples.
The corrected controller supplies the private library path and uses a separate results directory.

The normal candidate completes all 1,896 records in 147.091662380 seconds with exit zero.
User CPU time is 146.798298 seconds, and system CPU time is 0.255364 seconds.
Peak RSS is 222,788 KiB.
Its full TAP hash is `07c2de202ca09462f396f349cbf99df5b0f044194e9314840de8bf6331c29161`.
This output matches the compiled program output.

The [full control](full/results.json) has no paired baseline timing.
The normal full-corpus run does not measure allocation balance.

The diagnostic limit remains 1,980 seconds.
The CI package budget remains 33 minutes.
The corpus, exclusions, proof rules, negative controls, and tolerances stay unchanged.
Complete exact-head CI remains the merge gate.

## Reproduction and limits

Use isolated worktrees at the recorded revisions and the same qualified producer.
The [preparation script](prepare.py), [measurement script](measure.py), and [compilation script](compile-controls.py) retain every command.
Their absolute paths identify the original worktrees.
The [controller](collect.py) waits for builds and targeted validation before subset timings.

Compilation follows subset controls, then full interpretation, then instruction counts.
The TLS control follows that original sequence.
The additional complete-compilation instruction control follows the TLS control.
The shared host limits timing precision.

Executables and proof caches stay outside this archive.
The ignored originals remain under `build/interpreter-lazy-main-qualification-20261110-0710` in the primary checkout.
The [manifest](manifest.json) hashes the retained data files.
The [compression identities](compression-identities.json) preserve original raw bytes with lossless gzip.
The README and manifest itself stay outside the data hash list.

Verify local files with `python3 verify.py`.
Verify committed blobs with `python3 verify.py --commit HEAD`.
Do not pool results from different compiler sources.
These measurements establish no external rank or full-corpus speedup factor.
