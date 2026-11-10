# Argument prepass after the upstream Map index

Status: [record] rejected prepass from 2026-11-10.

The baseline is main `da3282d0f3bc2614d45667b17edad8f844eb6a1b`.
The candidate is `70171cab7f9b7c0ed80ff26132b3e4d16425c406`.
Both retain the complete `Funcs` Map index from upstream PR #15080.
The candidate adds the earlier unused-search shortcut and regression controls.
Its argument prepass searches for a nested call before argument evaluation.
The [compressed patch](implementation.patch.gz) records the exact product delta.

The fixed input and standard library use `ebb50c78c0ca714180704352881da7e39efb570f`.
Both compilers use qualified producer `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
The [identity record](identities.json) retains source trees, input hashes, and the Go version.
Earlier `NameIndex` measurements do not describe this comparison.

## Decision

Four of five first-group pairs increase CPU time.
Two second-group pairs decrease CPU time, and one increases it.
Allocation counts decrease on both groups.
These results do not justify a general interpreter speedup claim.

The extra argument prepass is rejected as the final implementation.
A separate trial defers lookup until the first nested argument call.
That trial needs its own measurements and correctness controls.

## Builds and validation

Normal executables omit debug variables and embedded source files.
They disable profiles and allocation counters and retain ownership certification.
Separate counter executables enable allocation counts.
The [preparation record](prepare.json) retains all four successful fresh builds and their identities.

The [validation record](validation/validation.json) retains the successful pinned source check and read-only format check.
Eleven targeted suites pass in 228.670 seconds.
The [test log](validation/tests.log) contains 388 passing test and subtest results and zero skips.
These suites include x86-64, ARM64 under QEMU, and WebAssembly.
The same-count REPL replacement retains the earlier session.
The upstream 4,000-function cost guard also passes.

## Interpreter comparisons

Five pairs alternate compiler order on the first 32-record group.
Three pairs alternate order on the second group, with 127,126 decoded bytes.
All sixteen normal commands exit zero with identical TAP output within each group.
The [raw records](subsets/results.json) retain every sample and all outliers.
CPU time is the sum of each child’s user and system CPU time.

| Measurement | Main baseline | Prepass candidate |
| --- | ---: | ---: |
| First group median wall time | 3.643474598 s | 3.773858871 s |
| First group median CPU time | 3.640367000 s | 3.756044000 s |
| First group median peak RSS | 148,416 KiB | 148,416 KiB |
| First group wall range | [3.52652112, 3.700166256] s | [3.45034749, 4.294025774] s |
| Second group median wall time | 6.668403699 s | 6.080750709 s |
| Second group median CPU time | 6.652330000 s | 6.070450000 s |
| Second group median peak RSS | 151,232 KiB | 151,232 KiB |
| Second group wall range | [6.423245168, 6.669173727] s | [5.834018458, 6.926564315] s |

Paired CPU changes for the first group are -2.2147%, +17.7710%, +4.5407%, +3.4396%, +2.9784%.
Paired CPU changes for the second group are -8.7470%, -9.2485%, +4.0864%.
Negative values indicate less CPU time for the candidate.
These subset results establish no external rank or full-corpus speedup factor.

## Allocation controls

| Group | Main baseline | Prepass candidate | Fewer events |
| --- | ---: | ---: | ---: |
| first | 29,143,636 | 28,629,458 | 514,178 |
| second | 79,975,548 | 66,612,263 | 13,363,285 |

Every counter run frees all allocations and reports zero live bytes.
Counter executables are separate from normal timing executables.

## Complete compilation controls

Both compilers build the complete fixed Opus reference program and its imports to a native executable.
Three pairs alternate compiler order per cache mode.
Each compiler primes its own proof cache.
The separate report probes record 112 hits and zero misses.
Actual measurements use normal verifier defaults without the report flag.

| Measurement | Main baseline | Prepass candidate |
| --- | ---: | ---: |
| cold median wall time | 4.342659613 s | 4.273505498 s |
| cold median CPU time | 4.334156000 s | 4.269169000 s |
| cold median peak RSS | 163,408 KiB | 163,408 KiB |
| warm median wall time | 4.076260544 s | 4.023878646 s |
| warm median CPU time | 4.075415000 s | 4.020636000 s |
| warm median peak RSS | 163,024 KiB | 163,024 KiB |

All sixteen compilation commands exit zero and produce identical native output.
The [compilation record](compilation/results.json) retains every command, sample, output hash, and cache probe.
The executable runs successfully and produces the same complete TAP output as the interpreter.
These controls establish no consistent compiler timing gain.
Peak RSS medians remain equal within each comparison.

The normal candidate completes all 1,896 records in 132.454933768 seconds with exit zero.
Peak RSS is 222784 KiB.
The [full control](full/results.json) retains child resource measurements and output agreement.
Its diagnostic limit remains 1,980 seconds.
There is no paired full-corpus baseline timing.

## Reproduction and limits

Use isolated worktrees at the recorded revisions and the same qualified producer.
The [preparation script](prepare.py), [measurement script](measure.py), and [compilation script](compile-controls.py) retain complete commands.
The [controller](run.sh) waits for successful validation and all builds before measurements start.

Builds overlap the targeted tests, outside the timed intervals.
Subset measurements precede complete compilation, which precedes full interpretation.
The shared host limits timing precision.
No sample is removed because of its value.

The codec, corpus, exclusions, package budget, ownership certification, proof rules, negative controls, and tolerances stay unchanged.
Exact-head complete CI remains the merge gate.

The [manifest](manifest.json) hashes 113 data files.
The README and manifest itself are outside that hash list.
The [compression identities](compression-identities.json) preserve the original bytes of lossless gzip records.
Executables and proof caches remain in the ignored original build directory.
