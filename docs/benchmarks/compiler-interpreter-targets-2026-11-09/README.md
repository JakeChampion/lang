# Interpreter target searches

Status: [experiment] completed diagnostic subset comparison from 2026-11-09.

The baseline compiler source is `ebb50c78c0ca714180704352881da7e39efb570f`.
The candidate source is `424fe24f`.
Both use producer `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
The fixed input and standard library use the baseline source.
The builds omit debug variables and embedded source files.
Ownership certification stays enabled.

## Problem and change

The native interpreter exceeded the existing CI package budget during `opus_delimited`.
An earlier main run at `d04e79179f` failed with the same active test before the semantic liveness change.
The stack waits for the native interpreter after the Go oracle returns.
The two complete CI logs retain these failures.
The package abort explains the other unknown test outcomes in those logs.

A fresh CPU profile uses the first 32 records from the unchanged reference corpus.
It contains 571 samples and zero dropped samples.
`call_target` accounts for 78 exclusive samples, or 13.66%.
There are 167 stacks at the 64-frame limit.
Inclusive attribution can overlap and truncate.

The interpreter searches declarations before argument evaluation.
It uses the result for nested call arguments and implicit type arguments of free calls.
The candidate omits searches whose result neither path uses.
The actual dispatch and argument order stay unchanged.
The two addressability checks retain their declaration searches.

The [implementation patch](implementation.patch) includes five regression cases.
They cover explicit type arguments, zero arguments, implicit constant type arguments, and nested generic method and associated calls.
Both normal binaries return the expected results for all five cases.
They also return identical failures and diagnostics for five negative cases.
Two earlier test designs failed the baseline and remain as diagnostic records.

## Completed subset comparison

Each sample runs the complete interpreter command on the first reference group.
This diagnostic input contains 32 packet records, rather than the full 1,896-record corpus.
The normal binaries disable CPU profiling and allocation counters.
Five pairs alternate compiler order.
All ten commands exit zero and produce identical TAP output.

| Measurement | Baseline | Candidate |
| --- | ---: | ---: |
| Median wall time | 18.842584233 s | 17.350765071 s |
| Wall time range | 17.724580025–28.396457764 s | 15.542478475–23.719385198 s |
| Median user plus system CPU time | 18.692494 s | 15.842723 s |
| CPU time range | 17.533600–20.224722 s | 14.897656–17.209412 s |
| Median peak RSS | 148,164 KiB | 148,168 KiB |

The CPU time decreases in every pair, with reductions from 11.18% to 20.48%.
The host also runs full-corpus controls, compiler builds, and targeted tests during this comparison.
The raw samples retain every value.
The [concurrency note](concurrency-note.json) describes these overlaps.
These results establish a benefit for this diagnostic subset only.
They do not establish full-corpus completion, a complete-compilation gain, or an external rank.

The [larger profile](larger-profile/second-group-breakdown.json) uses the second reference group, with 32 records and 127,126 decoded bytes.
It exits normally with the expected TAP result.
The reservoir retains 8,192 samples and omits 5,257 samples, including replacements.
There are 5,685 retained samples at the 64-frame limit.
`call_target` accounts for 5,269 exclusive samples, or 64.32% of retained samples.
These reservoir and frame limits constrain attribution.

The [corpus census](larger-profile/corpus-census.json) records all 60 groups, 1,896 packet records, and 4,792,877 decoded bytes.
The larger profile remains diagnostic evidence, rather than a paired time comparison.
Its first collector stopped before execution because it read an incomplete monitor record.
The archive retains that failure and the complete retry separately.

Three further pairs alternate compiler order on that second group with both normal binaries.
All six commands exit zero and produce identical TAP output.
The [larger comparison](larger-comparison/second-group-summary.json) retains every sample.
CPU time decreases in each pair, by 29.67%, 43.62%, and 7.02%.
The baseline median wall time is 164.218644072 seconds, and the candidate median is 103.929773325 seconds.
Concurrent workloads and wide time ranges limit these results to the measured subset.

The [candidate profile](candidate-profile/breakdown.json) records 5,675 samples and zero drops on the same larger diagnostic group.
There are 3,957 samples at the 64-frame limit.
`call_target` still accounts for 2,805 exclusive samples, or 49.43%.
`runtime_method_name` accounts for 959 exclusive samples, or 16.90%.
These diagnostics identify further lookup work to investigate.
They do not measure a further optimization.

The callable-value suite and the complete x86-64 and ARM64 driver suites report passes in [the test log](tests-corrected.log).
The tool lost that parent session before the last two Go suites finished.
The [session record](session-loss.json) retains this interruption without an overall suite verdict.
Both normal binaries also pass those two generic fixtures through direct CLI commands in [the generic controls](generic-controls.json).

The detached retry completes all five Go suites with exit zero in 371.039 seconds.
It reports 325 passing test and subtest results with zero skips.
Go vet and the CI self-tests also pass.

The five new call cases also pass through a WASI interpreter driver under Wasmtime 50.0.1.
Each case returns the expected status, frees every allocation, and reports zero live bytes.
The [WASI controls](wasi-controls/results.json) retain the source, producer, module hash, commands, and complete census output.
This driver check makes no WebAssembly performance claim.

Separate allocation-counter builds reduce allocation events from 29,232,928 to 28,676,955 on the same subset.
The reduction is 555,973 events, or 1.90%.
Both runs free every allocation, report zero live bytes, and produce identical TAP output.
The [allocation controls](allocation-controls/count-controls.json) retain both build identities and complete commands.
The original allocation collector stopped without a build outcome, and its incomplete records remain in this archive.

The unchanged normal baseline reaches the full-corpus deadline without completion.
The [completion record](full-baseline-procfs-control.json) retains the timeout and exit status after the parent-session loss.
The full candidate also reaches its diagnostic deadline without completion.
The [full-corpus controls](full-corpus-controls/orphan-controls.json) retain both timeout outcomes and the status of each orphan process.
The different concurrent workloads prevent a paired full-corpus time comparison.
The detached full-corpus monitor retains the original deadlines after the parent-session loss.

The direct full-corpus controls use a 33-minute diagnostic limit.
The test corpus, exclusions, and CI package budget stay unchanged.
Exact-head CI must still validate the package budget with its other work.
Later records must state each completed outcome explicitly.

The unused-search shortcut improves both measured subsets but does not repair the full-corpus timeout.
The current investigation retains this result and examines an immutable function-name index.
No index implementation or performance result is present in this archive revision.

## Complete compilation controls

The [compilation controls](compilation-controls/samples.json) compile the fixed baseline checker and its imports to a native executable.
They use the same normal binaries, input, standard library, producer, and target as the interpreter comparison.
Cold commands disable the proof memo.
Warm commands use separate caches that each binary primes itself.
Each reporting probe records 2,410 hits and zero misses.
The measured commands use normal verifier defaults without the reporting flag.

Three pairs alternate compiler order in each mode.
All 16 commands, including prime and probe controls, exit zero and produce the same executable hash.
The [summary](compilation-controls/summary.json) retains that hash and the resource measurements.
Other interpreter workloads run concurrently.

| Measurement | Baseline | Candidate |
| --- | ---: | ---: |
| Cold median wall time | 19.976808707 s | 22.338687408 s |
| Cold wall time range | 19.716516333–36.005140447 s | 21.385110076–37.518412736 s |
| Cold median CPU time | 19.953062 s | 22.301943 s |
| Cold median peak RSS | 585,412 KiB | 585,480 KiB |
| Warm median wall time | 29.515208713 s | 19.267208210 s |
| Warm wall time range | 19.021349127–33.884395873 s | 17.434774872–33.451700101 s |
| Warm median CPU time | 29.514153 s | 19.249595 s |
| Warm median peak RSS | 582,788 KiB | 582,664 KiB |

The cold median increases and the warm median decreases.
These variable samples do not establish a complete-compilation performance change.
The archive retains every sample and all raw logs.
The native output hash is `ca913adb4c4164b7b49e1b2c73e62739c178d9bc05978b3ccdc30138fdbca068`.

## Identities and reproduction

The [identity record](identities.json), [candidate build](build.json), and [baseline builds](profile-builds.json) retain exact sources, hashes, and commands.
The [comparison collector](run-comparison.py) retains exact commands, environment, budgets, and child resource measurements.
Its paths refer to the original worktrees.
Use corresponding isolated worktrees for a new reproduction.
Retain the same fixed input and standard library for both binaries.

Build the producer through `bootstrap/bootstrap.sh stage0` with the source pin above.
Build each normal CLI with `FERN_PROFILE=0`, `FERN_LEAKCHECK=0`, `FERN_RC_CERTIFY=1`, and `FERN_RC_CERTIFY_MEMO=0`.
Use the build arguments in the identity records without `-g` or `-embed`.
Copy [the diagnostic input](first-group.fern.txt) to `build/interpreter-profile-20261109/first-group.fern` under the fixed input tree.
This location preserves its relative vector import.
Run five alternating pairs with each normal CLI and the fixed standard library.

The CPU profile uses a separate executable with `FERN_PROFILE=cpu` at build time.
Its emitted assembly has zero instruction references to allocation counters.
The [counter audit](profile-counter-audit.json) retains the assembly hash.
The assembly itself was not executed for measurement.
The large assembly and executable files remain outside this archive.

The [manifest](manifest.json) hashes each data file.
This README and the manifest itself are outside that hash list.
