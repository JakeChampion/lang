# Interpreter index after upstream repairs

Status: [record] separate qualification from 2026-11-10.

The baseline is main `f9904974a77249949607f3b6196e489bf53c3602`.
The candidate is `206df7eeb094f5540ce1bd25cc2bd8090dae5df2`.
The fixed inputs and standard library use `ebb50c78c0ca714180704352881da7e39efb570f`.
Both compilers use qualified producer `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
The [identity record](identities.json) retains source trees and input hashes.

Main now includes native lexer, ownership, bit-demand, shift, and unsigned-guard changes.
The callback repair from upstream PR #15027 removes the earlier Android Plex blocker.
This comparison measures the existing unused-search shortcut and declaration index against this newer main.
It does not transfer totals from the [earlier main comparison](../compiler-interpreter-index-main-2026-11-09/README.md).
The [compressed patch](implementation.patch.gz) preserves the exact product delta and regression tests relative to the new baseline.

## Builds and correctness

Normal executables omit debug variables and embedded source files.
They disable profiling and allocation counters and retain ownership certification.
Separate counter executables enable allocation counts.
The [preparation record](prepare.json) retains three successful fresh builds and the reused normal baseline build.
The [baseline record](baseline-build-record.json) retains that earlier build with the same source, producer, flags, and executable hash.
Its unchanged Android Plex source check exits zero with empty diagnostics.

The [pinned source check](validation/source-check-result.json) and [ten targeted suites](validation/tests-result.json) exit zero.
The [test log](validation/tests.log) contains 387 passing test and subtest results and zero skips.
The Go package completes in 204.280 seconds.
The suites include x86-64, ARM64 under QEMU, and WebAssembly.
The REPL regression retains the earlier session after replacement with the same declaration count.

These tests ran on the merged working tree before its commit.
The final candidate retains that entire tree byte for byte.
The identity record proves tree equality with tested commit `3b9ed9c54ccc8e12b21847597eca5bdc49db53b8`.
The [source audit](source-audit.json) confirms clean immutable snapshots and unchanged earlier measurement archives.

The normal candidate completes all 1,896 records with exit zero in 364.440146008 seconds.
User CPU time is 364.063290000 seconds, and system CPU time is 0.253260000 seconds.
Peak RSS is 222,848 KiB.
The [full control](full/results.json) retains exact commands, identities, output hashes, and child resource measurements.
Its diagnostic limit remains 1,980 seconds.
This control has no paired full-corpus baseline timing.

## Interpreter comparisons

Five pairs alternate compiler order on the first 32-record group.
Three pairs alternate order on the second group, with 127,126 decoded bytes.
All sixteen normal commands exit zero and produce identical TAP output within each group.
The [raw records](subsets/results.json) retain every sample and all outliers.
CPU time is the sum of each child's user and system CPU time.

| Measurement | Main baseline | Candidate |
| --- | ---: | ---: |
| First group median wall time | 6.022223948 s | 4.330360434 s |
| First group wall time range | 5.692393895–7.519366496 s | 4.139098686–4.664741350 s |
| First group median CPU time | 6.017540000 s | 4.322751000 s |
| First group median peak RSS | 141,008 KiB | 148,480 KiB |
| Second group median wall time | 66.187300611 s | 11.546062087 s |
| Second group wall time range | 60.948457820–67.258952347 s | 10.851962638–11.550514237 s |
| Second group median CPU time | 66.166213000 s | 11.530690000 s |
| Second group median peak RSS | 143,696 KiB | 151,168 KiB |

Paired CPU changes on the first group are -43.6355%, -17.8868%, -29.8042%, -26.9658%, -37.7961%.
Paired CPU changes on the second group are -82.5608%, -81.0763%, -83.8719%.
Negative values indicate less CPU time for the candidate.
These subset results establish no external rank or full-corpus speedup factor.

Median peak RSS increases by 7,472 KiB on both interpreter subsets.
The complete-compilation controls also increase median peak RSS by 3,004 KiB in each cache mode.
These memory costs accompany the lower interpreter CPU times and allocation counts.
The measurements do not identify the cause of the RSS increase.

## Allocation controls

| Group | Main baseline | Candidate | Fewer events |
| --- | ---: | ---: | ---: |
| First 32 records | 29,335,971 | 28,687,281 | 648,690 |
| Second 32 records | 86,368,676 | 66,340,802 | 20,027,874 |

Every counter run frees all allocations and reports zero live bytes.
Each pair produces identical TAP output.
Counter executables are separate from the normal measurement executables.

## Complete compilation controls

Both compilers build the complete fixed Opus reference program and its imports to a native executable.
Three pairs alternate compiler order per cache mode.
Each executable primes its own proof cache.
The [reporting probes](compilation/results.json) record positive hit counts and zero misses.
Actual measurements use normal verifier defaults without the reporting flag.

| Measurement | Main baseline | Candidate |
| --- | ---: | ---: |
| Cold median wall time | 4.086566019 s | 4.051083027 s |
| Cold wall time range | 4.008517667–4.279215993 s | 3.983980081–4.068390904 s |
| Cold median CPU time | 4.084727000 s | 4.049333000 s |
| Cold median peak RSS | 160,336 KiB | 163,340 KiB |
| Warm median wall time | 4.109685534 s | 4.254744357 s |
| Warm wall time range | 3.963643680–4.188955468 s | 3.907106649–5.435663652 s |
| Warm median CPU time | 4.108890000 s | 4.250799000 s |
| Warm median peak RSS | 159,952 KiB | 162,956 KiB |

All sixteen commands exit zero and produce the same native executable.
Its SHA-256 is `e8ddd643bdc9d72008d930fc42ee8f1574d6f39ae3628891ea0f828f3c67b46f`.
The executable runs successfully and produces the same complete TAP output as the interpreter.
These controls establish output agreement and retain the complete-compilation costs.
They do not establish a general compiler performance gain.

## Measurement limits and reproduction

The [controller](run.sh) runs builds, subset timings and counts, complete compilation, then full interpretation in sequence.
The earlier source and targeted tests finish before these measurements start.
The reused baseline build overlaps those earlier tests, outside the measured interval.
The shared host still limits timing precision.
All source pairs retain separate results, and no sample is removed because of its value.

Use isolated worktrees at the recorded revisions and the same qualified producer.
The [preparation script](prepare.py), [measurement script](measure.py), and [compilation script](compile-controls.py) retain complete commands.
Their paths refer to the original worktrees.
Preserve the fixed input, standard library, compiler order, flags, and cache state in a repeat.
The codec, corpus, exclusions, CI package budget, ownership certification, proof rules, negative controls, and tolerances stay unchanged.
Exact-head CI remains the merge gate.

The [manifest](manifest.json) hashes each data file.
The README and manifest itself are outside that hash list.
The [compression identities](compression-identities.json) verify the original bytes of lossless gzip records.
Executables and cache files remain outside this archive.
The ignored evidence remains under `build/interpreter-main-qualification-20261110-0230` in the original worktree.
