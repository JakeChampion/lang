# Compiler performance

Status: [plan] measurement process and priorities, updated 2026-11-09.

Fern aims to have one of the fastest parsers and self-hosted compilers in the world.
Correct diagnostics, ownership proofs, and generated code remain requirements.
The [parser record](PARSER-PERFORMANCE.md) describes the first article-based changes.
The [profiling reference](PROFILING.md) describes the native CPU and allocation profilers.

## Measurements

| Workload | Main measurements | Required controls |
| --- | --- | --- |
| Tokenize and parse | Source bytes per second, allocations, peak memory | Real source corpus and malformed input |
| Cold compilation | Wall time, CPU time, peak memory, allocation count | No prior proof or module cache |
| Warm compilation | Same measurements as cold compilation | Separate caches for each compiler |
| Edit and rebuild | Latency after a local edit and an imported API change | Explicit dependency invalidation |
| Generated programs | Execution time, instructions, allocations, binary size | Same source, output, target, and optimization policy |
| Bootstrap | Compiler build time and peak memory | Qualified producer and complete fixpoint |

The current harness measures cold and warm proof caches.
It does not yet measure incremental module compilation or edit latency.
Parser microbenchmarks and full compilation answer different questions.
Neither result alone establishes a rank against other compilers.

Use the same host and fixed input for each pair.
Alternate the compiler order and retain every sample.
Record source revisions, compiler hashes, producer, target, flags, and cache policy.

Build production measurements without `-g` because debug variables can prevent scalar replacement.
Enable the profiler through `FERN_PROFILE` only when the producer builds the profiling executable.
Keep ownership certification enabled in cold and warm comparisons.
Disable only its memo with `FERN_RC_CERTIFY_MEMO=0` for the cold comparison.

Use exact allocation counts or instruction counts when wall time varies on a shared host.
Allocation profiles identify candidates but contain sampling error.
Retain separate results for ARM64, x86-64, and WebAssembly.
Use real ARM64 hardware for timing claims, rather than emulator wall time.

An external comparison must describe the work that each compiler performs.
Record diagnostics, optimization level, dependencies, cache state, and generated-code quality.
Publish results for each workload rather than a single score across different language semantics.

## Current profile

A production-mode compiler compiled the fixed checker and imports on Linux x86-64 with the proof cache disabled.
The baseline source revision is `d4636805f696a93b35f8461d8f29cb477d682a1c`.
The CPU profile contains 1,106 samples at 100 Hz, with no dropped samples.
These inclusive percentages describe one diagnostic run, not a benchmark result.
Nested functions can overlap, so the percentages are not additive.

| Function | Inclusive CPU samples |
| --- | ---: |
| `semlower.substitution` | 36.7% |
| `irverifygate.certify_or_refuse` | 24.1% |
| `checker.check_module_pass` | 11.6% |
| `seminline.inline_leaves` | 8.7% |

The allocation profile attributes about 47.9% of its estimated bytes to ownership certification.
Its reservoir retains 8,192 stacks and drops 789,488 selected events.
Systematic sampling and reservoir scaling limit the precision of that estimate.
The first change reuses graph positions and derives reachability from dominator results during certification.
It retains the proof rules and computes fresh results for each extracted graph.

The [fixed-input comparison](benchmarks/compiler-graphs-2026-11-07/README.md) records 45,563 fewer cold allocation events and 13,342,618 fewer warm instructions.
These are small reductions of 0.0875% and 0.0448%, respectively.
The wall-time samples do not establish a speedup.

The [inliner comparison](benchmarks/compiler-inliner-2026-11-08/README.md) adds direct mask allocation and cached value classification.
The combined changes remove 88,545 cold allocation events and 354,975,385 warm instructions from the original fixed checker comparison.
These reductions are 0.1700% and 1.1925%, respectively, with identical generated output.
The inliner comparison retains the rejected worklist trial and the direct-allocation control.
Fresh heap growth remains unchanged relative to the graph-only parent.

A new profile at `49056c6e39` contains 1,093 CPU samples and no dropped samples.
Semantic substitution accounts for 36.69% of inclusive samples, and ownership certification accounts for 20.77%.

The [liveness comparison](benchmarks/compiler-liveness-2026-11-09/README.md) reuses block positions and definition sites that the certifier already holds.
It removes 16,644 allocations and 125,065,828 cold guest instructions from the fixed checker compile.
The reductions are 0.0317% and 0.3003%, respectively.
Warm guest instructions decrease by 11,966,410, or 0.0406%.
Both comparisons retain ownership certification and produce identical output.

The [updated liveness comparison](benchmarks/compiler-liveness-main-2026-11-09/README.md) checks the same change after the merge of main `d04e79179f`.
The candidate code source is `2d7d444e94`.
It removes 16,652 cold allocation events, 125,074,176 cold guest instructions, and 11,855,020 warm guest instructions.
The reductions are 0.0317%, 0.3004%, and 0.0402%, respectively.

Allocation runs emit assembly, and instruction runs emit native executables.
The output hashes match within each comparison.
The allocation runs free all allocations and report zero live bytes.

These work counts do not establish a wall-time speedup or an external rank.
The earlier archives retain their original source identities.
Do not add reductions from different snapshots.

A [fresh warm profile](benchmarks/compiler-main-profile-2026-11-09/README.md) uses compiler source `6bca1d48f3` and the same fixed input `d04e79179f`.
It contains 1,095 CPU samples and no dropped samples.
Semantic substitution accounts for 46.67% of inclusive samples.
Its separate cache probe confirms 2,391 hits and zero misses.
The profile and metadata audit identify possible reuse of checked graph facts in semantic liveness.
No further optimization follows from this diagnostic profile alone.

The [semantic liveness comparison](benchmarks/compiler-semantic-liveness-2026-11-09/README.md) reuses maps from checked graph facts.
The baseline compiler and fixed input use `12be8f1186`, and the candidate code uses `3a296b887c`.
Cold allocation events decrease by 17,277, or 0.0327%.
Cold guest instructions decrease by 29,162,065, or 0.0697%.
Warm guest instructions decrease by 29,097,267, or 0.0980%.
The output hashes match, and the allocation runs report zero live bytes.

Five alternating pairs per cache mode also measure complete native compilation.
Median peak RSS decreases by 1,276 KiB cold and 1,020 KiB warm.
Candidate wall-time medians are slightly slower, with mixed paired differences and substantial variation.
These samples do not establish a wall-time or CPU-time speedup.
The complete stage-2 and stage-3 compiler executables match byte for byte with certification enabled and the proof cache disabled.

The [separate main comparison](benchmarks/compiler-semantic-liveness-main-2026-11-09/README.md) uses baseline `a19e87208c` and candidate `7dbc615c9c`.
The fixed input remains `12be8f1186`.
Cold allocation events decrease by 17,277, with balanced frees and zero live bytes.
Cold guest instructions decrease by 29,160,452, or 0.0697%.
Warm guest instructions decrease by 29,066,411, or 0.0979%.
Output hashes match within each comparison.

Five alternating pairs per mode retain every timing sample.
Candidate cold medians are slightly lower, and warm medians are higher.
The data establish no wall-time or CPU-time speedup.
Median peak RSS decreases by 1,280 KiB cold and 1,152 KiB warm.
The archive records the overlap of CI self-tests with the first cold baseline sample.
Stage-2 and stage-3 compiler executables match byte for byte.

## Priorities

1. Remove repeated graph analysis and allocation in ownership certification.
   Reuse results only while the graph remains unchanged.
   Evaluate compact bitsets and worklists for repeated dataflow propagation.

2. Reduce repeated semantic planning and function analysis in `semlower.produced_rows`.
   Measure schema views, ownership-mode inference, and selective re-lowering before changes.
   Evaluate canonical type IDs where type lookups dominate.

3. Reduce temporary arrays, boxed tuples, and repeated AST traversal across compiler passes.
   Prefer a compiler or runtime improvement when every program can benefit.

4. Evaluate compact token, AST, and IR storage with real source corpora.
   Measure SIMD scans, source slices, and phase-local allocation against their lifetime costs.

5. Measure output buffers and object emission after the earlier phases improve.

6. Evaluate deterministic module concurrency and persistent typed IR caches.
   Measure dependency invalidation, memory use, diagnostics, and cold performance before adoption.

Each change needs a focused correctness test and a fixed-input comparison.
Reject changes that weaken proofs or move costs outside the measured interval.
Retain negative results when a proposed technique does not improve the workload.
Reprofile after each material change because the next bottleneck can move.

## Comparison command

```sh
scripts/native-pair-bench BASE_COMPILER CANDIDATE_COMPILER FIXED_SOURCE_ROOT report.json --compiler-pairs 7
```

The harness uses separate temporary proof caches for the warm comparison.
Its `compiler_cold` field records the cold comparison.
Its `compiler` field retains the existing warm comparison key.
The [development loop](LOCAL-DEV-LOOP.md#native-compiler-and-x25519-comparisons) describes the complete build process.
