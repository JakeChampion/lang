# Compiler graph analysis, 2026-11-07

The change reuses block positions during ownership certification.
It also derives reachability from an existing dominator result.
Each extracted graph still receives fresh analysis.
The proof rules remain unchanged.

## Inputs and producer

Both compilers use the same published stage0 from `bootstrap/stage0.lock`.
The baseline source and fixed checker input use revision `d4636805f696a93b35f8461d8f29cb477d682a1c`.
The candidate compiler source uses revision `99e807b132ff7100613ef1d0f18d1fa52cf8d2e3`.
These builds use stage0 directly, rather than a new bootstrap fixpoint.
The [manifest](manifest.json) records source, binary, producer, and profile hashes.

The host runs Linux x86-64 on an Intel Xeon Platinum 8573C.
The compiler target is `x86-64-linux`.
Production compiler builds omit `-g`.
That flag materializes debug variables and can prevent scalar replacement.
The profiler emits its own symbols without that flag.

## Exact allocations

The producer builds both counter binaries with `FERN_LEAKCHECK=1`.
Both binaries compile the same checker and imports to assembly.
Ownership certification remains enabled through `FERN_RC_CERTIFY=1`.
The proof cache remains disabled through `FERN_RC_CERTIFY_MEMO=0`.
Both runs use the same output path and complete successfully.

| Measurement | Baseline | Candidate |
| --- | ---: | ---: |
| Allocation events | 52,096,031 | 52,050,468 |
| Free events | 52,096,031 | 52,050,468 |
| Live bytes at exit | 0 | 0 |
| Assembly bytes | 32,467,837 | 32,467,837 |

The candidate removes 45,563 allocation events, or 0.0875%.
This is a small reduction in complete compilation.
Both assembly files have SHA-256 `e961f3d9f4fdccc89dd5fe756d886d4db0375972c8371db5c156bb7b33a241f5`.
The [allocation report](allocations.json) contains the commands and exact results.
These counters measure allocation events, rather than peak memory or cumulative allocated bytes.

## Wall time

The comparison uses uninstrumented compilers with the same producer and target.
`scripts/native-pair-bench` alternates execution order and retains every sample.
The compiler modes use seven pairs each.
The generated X25519 and calendar programs use 15 pairs each.
Both compiler modes keep ownership certification enabled.
Each warm mode uses a separate proof cache and an initial run before its timed samples.

| Compiler mode | Baseline median | Candidate median | Median paired candidate/baseline ratio |
| --- | ---: | ---: | ---: |
| Cold proof cache | 12.499 s | 12.475 s | 0.9457 |
| Warm proof cache | 8.775 s | 9.351 s | 1.0518 |

The median paired ratio differs from the ratio of independent medians.
The cold paired ratios range from 0.831 to 1.183.
The warm paired ratios range from 0.932 to 1.125.
This shared-host trial does not establish a wall-time improvement.
It also does not resolve whether the higher warm ratio represents a regression or host variation.
The [timing report](timings.json) retains the complete observations, including both generated-program comparisons.

## Warm instruction count

Valgrind 3.24.0 Callgrind counts guest instructions for both production compiler builds.
Each compiler first populates a separate proof cache through a native run.
The counted runs use the same fixed input and output path.
Both runs keep ownership certification enabled and use their warm proof caches.
The build-stamp files retain one entry each, and both proof files contain 2,335 entries.
Cache simulation and branch simulation remain disabled.

| Measurement | Baseline | Candidate |
| --- | ---: | ---: |
| Guest instructions | 29,767,466,522 | 29,754,123,904 |

The candidate executes 13,342,618 fewer instructions, or 0.0448%.
Both native executables have SHA-256 `7f5f2f60c2a584c744b5f87e6e90f21cceb536bd104c7875e3681425a298bd87`.
The [instruction report](instructions.json) records the commands and binary hashes.
The archive also retains the [baseline log](before-callgrind.log) and [candidate log](after-callgrind.log).
This small reduction in execution work does not establish a wall-time gain.

For reproduction, populate separate proof caches with one native compile per compiler.
Run each compiler under Callgrind with its populated cache:

```sh
FERN_RC_CERTIFY=1 FERN_RC_CERTIFY_MEMO=CACHE valgrind --tool=callgrind --cache-sim=no --branch-sim=no --callgrind-out-file=counts.out COMPILER -target x86-64-linux -o checker FIXED_SOURCE_ROOT/compiler/checker.fern FIXED_SOURCE_ROOT/internal/stdlib
```

Check each exit status and the generated executable hash.
Read the instruction count from the `summary` field in each Callgrind output.

## Diagnostic profiles

The baseline profiling compilers use `FERN_PROFILE=cpu-pprof` or `FERN_PROFILE=alloc-pprof` during their build.
The fixed checker compile uses native executable output with certification enabled and the proof cache disabled.
The [profile commands](profile-runs.json) record these diagnostic runs.
Their durations include instrumentation and are separate from the timing comparison.

The [CPU summary](cpu-top.txt) contains 1,106 samples at 100 Hz with no dropped samples.
Ownership certification accounts for 24.1% of the inclusive CPU samples.
Semantic substitution accounts for 36.7%.
The [allocation summary](alloc-top.txt) attributes about 47.9% of estimated bytes to ownership certification.
Inclusive values can overlap and are not additive.

The allocation reservoir retains 8,192 stacks and drops 789,488 selected events.
Systematic sampling and reservoir scaling can bias the byte estimates.
The allocation profile identifies candidates but does not replace the exact allocation counter.
The manifest retains the raw profile hashes, while this archive retains the small text summaries.

## Reproduction

Create separate baseline and candidate checkouts at the recorded source revisions.
Build each compiler with the same stage0 and without `-g`.
Use each checkout's `compiler/fern.fern` entry and `internal/stdlib` directory.
Use the baseline checkout as `FIXED_SOURCE_ROOT` for both subjects.

```sh
scripts/native-pair-bench BASE_COMPILER CANDIDATE_COMPILER FIXED_SOURCE_ROOT timings.json --compiler-pairs 7
```

For exact allocations, build both compilers with `FERN_LEAKCHECK=1`.
Run each counter binary with these environment variables and arguments:

```sh
FERN_RC_CERTIFY=1 FERN_RC_CERTIFY_MEMO=0 COMPILER -target x86-64-linux -emit asm -o checker.s FIXED_SOURCE_ROOT/compiler/checker.fern FIXED_SOURCE_ROOT/internal/stdlib
```

Check each exit status before the counter result.
Compare the assembly hashes after each run.
Keep the output path identical for both compilers.

## Correctness checks

Dependency verification and selected ownership-certifier tests pass on x86-64 and WebAssembly.
The dependency verification driver also passes on ARM64 under QEMU.
The regression graph includes sparse block IDs, a nonzero entry position, a loop, and an unreachable self-loop.
It checks concrete dominators, independent reachability, and graph preservation.

The compiler source check, Go vet for `e2ecompiler`, and `make ci-selftest` pass.
The local Go overlay removes only the write to the unavailable `/proc/self/coredump_filter` file.
It retains `ulimit -c 0` and changes no compiler behavior.
