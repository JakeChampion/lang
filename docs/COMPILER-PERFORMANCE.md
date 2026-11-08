# Compiler performance

Status: [plan] measurement process and priorities, updated 2026-11-08.

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
