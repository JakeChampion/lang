# Interpreter index after main integration

Status: [record] independent qualification from 2026-11-09.

The baseline is main `4a04428264dd4641ca1032cf5edc84762a883dd5`.
The candidate is `6f6bddfc2af38b6c7e85d23d9338a10c8fb9c307`.
The fixed inputs and standard library use `ebb50c78c0ca714180704352881da7e39efb570f`.
Both use qualified producer `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
The [identity record](identities.json) retains the complete source identities.

Main includes an upstream extraction of interpreter, parser, and lexer test drivers and other compiler changes.
The merge preserves the upstream public driver entry points and the new immutable function-name index.
This archive measures the combined unused-search shortcut and index relative to current main.
It does not reuse totals from the [earlier index comparison](../compiler-interpreter-index-2026-11-09/README.md).
The [compressed patch](implementation.patch.gz) retains the code and regression tests relative to this baseline.
The [patch identity](patch-identity.json) records its original byte hash and preserves context whitespace through lossless gzip storage.

## Full correctness controls

The normal candidate completes the unchanged full 1,896-record corpus with exit zero in 431.159757258 seconds.
It uses 430.638142 seconds of user CPU time and 0.415468 seconds of system CPU time.
Peak RSS is 222,284 KiB.
The [direct control](full/results.json) retains the source hash, command, binary hash, output hashes, and child resource measurements.
The direct diagnostic limit stays at 1,980 seconds.
This control has no paired full-corpus baseline timing.

The original Go oracle/native `TestSelfHostStdTestInterp/opus_delimited` subtest also passes.
Its [test log](full-go.log) reports 634.93 seconds for the subtest and 695.687 seconds for the filtered package.
The [command record](full-go-result.json) retains the unchanged 33-minute package timeout.
No subtest reports a skip.
Other subtests remain outside this filtered control.

Ten [targeted suites](tests.log) pass, with 387 passing test and subtest results and zero skips.
The Go package completes with exit zero in 188.825 seconds.
These suites include both upstream extracted interpreter drivers on x86-64 and ARM64 under QEMU.
The scope and same-count REPL replacement controls also run on WebAssembly.
The other controls retain callable values, generic calls, associated constants, dynamic providers, and map key equality.

The [preparation record](prepare.json) retains both source checks and four successful normal or counter builds.
Normal executables omit debug variables and embedded source files, disable profiling and allocation counters, and retain ownership certification.
Separate counter executables enable allocation counts.
The codec, corpus, exclusions, CI budget, proof rules, negative controls, and tolerances stay unchanged.
Exact-head CI remains the merge gate.

## Repeated interpreter comparisons

Five pairs alternate compiler order on the first 32-record group.
Three further pairs alternate order on the second group, with 127,126 decoded bytes.
All sixteen normal commands exit zero and produce identical TAP output within each group.
The [raw records](paired/results.json) and [summary](summary.json) retain every value.
Concurrent builds and tests limit timing precision.

| Measurement | Main baseline | Candidate |
| --- | ---: | ---: |
| First-group median wall time | 6.445066962 s | 4.632106172 s |
| First-group wall time range | 5.854317127–6.529337829 s | 4.400517367–4.864404939 s |
| First-group median CPU time | 6.435965 s | 4.605921 s |
| First-group median peak RSS | 148,172 KiB | 148,300 KiB |
| Second-group median wall time | 77.661689026 s | 25.857951017 s |
| Second-group wall time range | 73.086989921–173.794730482 s | 13.562769170–29.852224589 s |
| Second-group median CPU time | 77.643750 s | 25.836215 s |
| Second-group median peak RSS | 150,860 KiB | 150,988 KiB |

CPU time decreases in each pair.
First-group reductions are 19.63%, 32.66%, 28.78%, 23.04%, and 25.74%.
Second-group reductions are 59.17%, 66.72%, and 92.20%.
The wide second-group ranges require caution, and no sample is removed.
These results apply to these measured subsets and establish no external rank.
Median peak RSS increases by 128 KiB on both groups.

## Allocation controls

| Group | Baseline events | Candidate events | Fewer events |
| --- | ---: | ---: | ---: |
| First 32 records | 29,235,985 | 28,587,295 | 648,690 |
| Second 32 records | 86,268,690 | 66,240,816 | 20,027,874 |

Each counter run frees every allocation and reports zero live bytes.
Each pair produces identical TAP output.
The raw records retain the exact counter executable hashes and commands.
These counter executables are separate from the normal timing executables.

## Complete compilation controls

This comparison compiles the full Opus reference program and its imports to a native executable.
It differs from the earlier checker-compilation workload, so those totals do not transfer.
Three pairs alternate compiler order in each cache mode.
Each binary primes its own proof cache, and both reporting probes record 112 hits and zero misses.
The measured commands retain normal verifier defaults without the reporting flag.
The [compilation summary](compilation-summary.json) retains the exact records.

| Measurement | Main baseline | Candidate |
| --- | ---: | ---: |
| Cold median wall time | 4.876149290 s | 4.836043758 s |
| Cold wall time range | 4.870993379–5.142244215 s | 4.740428374–5.019607887 s |
| Cold median CPU time | 4.873111 s | 4.834006 s |
| Cold median peak RSS | 163,532 KiB | 163,532 KiB |
| Warm median wall time | 4.753411844 s | 4.698797469 s |
| Warm wall time range | 4.726149438–5.128027639 s | 4.671396408–4.794486768 s |
| Warm median CPU time | 4.752885 s | 4.696269 s |
| Warm median peak RSS | 163,148 KiB | 163,148 KiB |

All sixteen compilation commands exit zero and produce the same native executable.
Its SHA-256 is `f3db053f31e511450898afc3ea6776613774f596703623a65e07520fc25af809`.
The emitted executable runs successfully and produces the same complete TAP output as the interpreter.
These timings under concurrent workloads do not establish a complete-compilation performance gain.

## Reproduction and retained files

Use isolated worktrees at the recorded source revisions and the same qualified producer.
The [preparation script](prepare.py) retains complete build arguments and environment values.
The [measurement script](measure.py), [targeted tests](tests.sh), [oracle control](full-go.sh), and [compilation collector](compile-controls.py) retain the exact commands.
Their paths refer to the original worktrees.
Keep the same fixed inputs, standard library, flags, cache policy, and compiler order for a new comparison.

The [manifest](manifest.json) hashes every data file.
The README and manifest itself are outside that hash list.
Executables and cache contents remain outside this archive.
The ignored evidence remains under `build/interpreter-main-qualification-20261109-2320` in the original worktree.
