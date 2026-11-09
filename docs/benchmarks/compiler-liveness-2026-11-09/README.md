# Compiler liveness comparison

Status: [record] measurements from 2026-11-09.

Ownership certification rebuilt block positions twice and definition sites for each liveness calculation.
The candidate reuses the maps that its `Walk` already holds.
The new entry point retains dependency and graph validation.
Each root graph and extracted graph receives fresh maps before the walk.
The maps must describe the same unchanged graph.

## Results

| Measurement | Baseline | Candidate | Change |
| --- | ---: | ---: | ---: |
| Cold allocation events | 52,453,768 | 52,437,124 | -0.0317% |
| Cold guest instructions | 41,648,447,069 | 41,523,381,241 | -0.3003% |
| Warm guest instructions | 29,490,210,760 | 29,478,244,350 | -0.0406% |

Both counter runs free every allocation and report zero live bytes.
The assembly output hashes match in the cold allocation comparison.
The native executable hashes match in all four instruction comparisons.
These results measure work on one fixed checker snapshot.

The [allocation data](allocations.json) and [instruction data](instructions.json) retain the exact counts.
The [manifest](manifest.json) records source revisions, compiler hashes, and data hashes.
The [source patch](reuse.patch) contains the compiler changes.

## Controls

The baseline source is `49056c6e392ba85b267e4abb6a44dd94872cc891`.
The candidate code source is `327c4e41111063446fa5ed897aa3dc5a557e4d98`.
Both compile `compiler/checker.fern` and imports from one fixed checkout of the baseline.
The host uses Linux x86-64 and an Intel Xeon Platinum 8573C processor.

One qualified stage0 producer builds all measurement binaries.
Its release tag is `stage0-20261107-38f241c`.
The builds omit `-g` and disable profiling for the measured instruction runs.
The allocation binaries use `FERN_LEAKCHECK=1` during their build.

Certification is enabled in every comparison.
Cold runs set `FERN_RC_CERTIFY_MEMO=0`.
Warm runs use separate primed proof caches with equal-length paths.
Each compiler uses the same output path within every comparison.

Cold instruction runs execute baseline then candidate.
Warm instruction runs execute candidate then baseline.
Valgrind counts the compiler process with child tracing disabled.
Each compiler has one completed instruction run per mode.

## Diagnostic profile

The baseline profiling compiler uses `FERN_PROFILE=cpu` during its build, without `-g`.
It compiles the same checker with certification enabled and the proof cache disabled.
The profile contains 1,093 samples at 100 Hz and no dropped samples.

Semantic substitution accounts for 36.69% of inclusive samples.
Ownership certification accounts for 20.77% of inclusive samples.
`ssalive.compute_checked_kept` accounts for 2.20% of exclusive samples.
The profile identifies candidates and does not measure the reduction in the table.
Inclusive sample percentages can overlap.

The [summary](cpu-top.txt) and [compressed raw profile](cpu.folded.gz) retain the diagnostic evidence.

## Validation

The liveness and certifier tests pass in 124.502 seconds.
The six liveness fixtures pass on x86-64, ARM64 under QEMU, and WebAssembly.
Their recorded solutions cover sparse blocks, loop phis, dependencies, and wide bitsets.
The new fixture has implicit parameters, an unused value, and an entry block after its successor in storage order.

Filtered live sets agree through the legacy and reused-map entry points.
Fourteen invalid-metadata cases retain their expected diagnostics.
The existing ownership-certifier cases pass on x86-64.

The compiler source check, Go vet for `e2ecompiler`, and `make ci-selftest` pass.
Local native tests use the existing coredump-filter overlay.
It removes the unavailable `/proc/self/coredump_filter` write and retains `ulimit -c 0`.

## Reproduction

Build each compiler with the pinned producer and its own source checkout:

```sh
FERN_PROFILE=0 FERN_RC_CERTIFY=1 FERN_RC_CERTIFY_MEMO=0 STAGE0 -target x86-64-linux -embed internal/stdlib -o COMPILER compiler/fern.fern internal/stdlib
```

Add `FERN_LEAKCHECK=1` to build each allocation binary.
Use the same fixed input checkout and output path for both allocation runs:

```sh
FERN_RC_CERTIFY=1 FERN_RC_CERTIFY_MEMO=0 COUNTER_COMPILER -target x86-64-linux -emit asm -o checker.s FIXED_ROOT/compiler/checker.fern FIXED_ROOT/internal/stdlib
```

Prime one proof cache per normal compiler before the warm instruction comparison.
Use `MEMO=0` for the cold comparison and the compiler's cache path for the warm comparison:

```sh
FERN_RC_CERTIFY=1 FERN_RC_CERTIFY_MEMO=MEMO valgrind --tool=callgrind --trace-children=no --cache-sim=no --branch-sim=no --callgrind-out-file=/dev/null COMPILER -target x86-64-linux -o checker FIXED_ROOT/compiler/checker.fern FIXED_ROOT/internal/stdlib
```

Check each exit status, allocation balance, and output hash.
