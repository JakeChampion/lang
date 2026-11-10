# Interpreter CI controls and baseline bank

Status: [record] measurements and validation from 2026-11-10.

These controls use PR head `7bd045eb04c8ea0b5980f0526e778ae107571d05`.
Its compiler and test code match measured source `6f6bddfc2af38b6c7e85d23d9338a10c8fb9c307`.
The baseline is main `4a04428264dd4641ca1032cf5edc84762a883dd5`.
Both normal compilers use qualified producer `a19e87208c5931d2600fdb75cc966fd179ae8c8f`.
The [identity record](identities.json) retains source identities and original byte hashes.

## ARM64 assembly count

[CI job 129524617582](https://github.com/JakeChampion/lang/actions/runs/42723690303/job/129524617582) reports `aarch64/video_qtrle.text` at 5,600, against the recorded 5,682.
The strict gate requires a baseline update for this decrease of 82 lines, or 1.44%.
The [decoded CI log](ci/perf-aarch64-129524617582.log.gz) retains the exact row and failure.
The baseline update retains existing tolerances and every dynamic instruction-count row.

Both qualified normal compilers independently emit 5,600 instruction lines for the same main-source benchmark and standard library.
Their complete assembly bytes match, with SHA-256 `6b8a995a9f2369ecc87656f882fc0ac81d9d39dd3181ebf54c56eab90f95a80c`.
The [paired records](reproduce/qtrle-results.json) retain commands, flags, compiler hashes, and the count rule from `scripts/perf-bench`.
The [baseline assembly](reproduce/qtrle-baseline.s.gz) and [candidate assembly](reproduce/qtrle-candidate.s.gz) retain their original bytes.
This bank records output that main already produces.
The control emits assembly and does not execute the QTRLE benchmark.

The existing performance-gate self-tests pass all 22 cases.
The [self-test log](ci/perf-gate-selftest.log.gz) retains that result.
An initial [single-row probe](ci/qtrle-gate.log.gz) against the complete baseline fails because 79 other metrics are absent.
That failure preserves the missing-metric check.
This archive does not claim a complete local ARM64 performance-lane pass.

## Plex ownership diagnostic

[Android CI job 129524621282](https://github.com/JakeChampion/lang/actions/runs/42723690303/job/129524621282) rejects `control.close(last)` in `examples/android/plex/app.fern:190` with E051.
The [decoded log](ci/android-stopwatch-129524621282.log.gz) retains the diagnostic.
The [paired reproduction](reproduce/results.json) checks the same unchanged main-source program and standard library with both qualified compilers.
Both return status 1 and produce byte-identical diagnostics.
Both diagnostic streams have SHA-256 `c001649922e47e408cf2bc3c5df6dfd03a4f4980ae67de7a1fcdb1993c853985`.

At collection time, [PR #15060](https://github.com/JakeChampion/lang/pull/15060) owns the callback-result repair.
The [existing claim](https://github.com/JakeChampion/lang/issues/15016#issuecomment-6754553399) directs this failure to that repair.
This baseline bank changes no ownership rule, negative control, or Android caller.
The PR remains draft until complete exact-head CI succeeds.

## Complete interpreter shards

The full current shard `0/4` passes all 72 selected subtests in 814.060 seconds, with zero failures or skips.
Within it, `opus_delimited` passes in 502.15 seconds and CSV passes in 723.70 seconds.
The [result](shard0/result.json), [raw test events](shard0/tests.jsonl.gz), and [command](shard0/run.sh) retain the complete control.
The original 33-minute package timeout, corpus, and exclusions stay unchanged.
This control establishes local completion within the package budget.

Current shard `3/4` separately passes all 70 selected subtests in 628.615 seconds, with zero failures or skips.
Its [result](shard3/result.json) and [raw events](shard3/tests.jsonl.gz) retain the other complete shard.
Corpus additions moved Opus from historical shard 3 to current shard 0.
The [selection correction](shard3/selection-correction.json) retains the initial scope mistake and current assignment.
Terminal event counts include the parent test and package, in addition to selected subtests.

## Complete source gate

The complete `make check-sources` gate returns zero.
Its [result](source-gate/result.json) and [log](source-gate/check-sources.log.gz) retain the exact command and outcome.
The runtime-module driver checks all seven configured targets.
The feature-census and fixture-source package passes in 166.880 seconds.
The compiler source, standalone standard-library imports, and all 100 auxiliary drivers pass their source checks.

## Residual CPU profile

The separate counter-free CPU build and second-group diagnostic both return zero.
The profile uses the same qualified producer and fixed `ebb50c78` input and standard library.
Its TAP bytes match the normal candidate control.
The [folded profile](profile/run.stderr.gz) contains 1,063 samples, with zero dropped samples.
Of these, 663 stacks reach the 64-frame limit and omit outer roots.
The [breakdown](profile/breakdown.json) retains exclusive and overlapping inclusive counts.

The initial controller incorrectly counts only stacks with the `_start` root, then fails its analysis assertion after execution succeeds.
The [controller failure](profile/controller-failure.log.gz) and original [execution record](profile/result.json) remain unchanged.
The separate [analyzer](profile/analyze.py) includes every folded row and verifies the total against the profile header.
No benchmark rerun or sample rejection follows that correction.

`eval_expr_call_expect` accounts for 393 exclusive samples and the generic box allocator accounts for 110.
The profile includes one inclusive sample in `call_target` and two exclusive samples in `resolve_ident_value`.
A qualified-name declaration scan remains a candidate for a later causal experiment.
These counts do not assign all call-expression samples to that scan or establish another speedup.
The [research notes](profile/research-notes.json) retain the limits and unchanged implementation scope.

## Reproduction and integrity

The reproduction scripts retain the exact paths from execution.
Recreate worktrees at the source identities above before you adapt those paths.
Use the qualified normal binaries from the [main comparison](../compiler-interpreter-index-main-2026-11-09/README.md).
Set `TASK_FIXED_INPUT` to the main `4a044282` worktree.
Set `TASK_BASE_COMPILER` and `TASK_CANDIDATE_COMPILER` to the matching normal binaries.

```sh
export FERN_PROFILE=0 FERN_LEAKCHECK=0 FERN_RC_CERTIFY=1 FERN_RC_CERTIFY_MEMO=0
"$TASK_BASE_COMPILER" -target arm64-linux -stdlib "$TASK_FIXED_INPUT/internal/stdlib" "$TASK_FIXED_INPUT/bench/video_qtrle.fern" > base.s
"$TASK_CANDIDATE_COMPILER" -target arm64-linux -stdlib "$TASK_FIXED_INPUT/internal/stdlib" "$TASK_FIXED_INPUT/bench/video_qtrle.fern" > candidate.s
grep -c '^[[:space:]]' base.s candidate.s
cmp base.s candidate.s
python3 docs/benchmarks/compiler-interpreter-ci-2026-11-10/verify.py
```

[The manifest](manifest.json) hashes every data file.
Gzip records preserve original log, assembly, and test-event whitespace.
The identity record retains each original byte count and SHA-256.
The full-shard and source-gate records provide correctness controls, rather than paired performance comparisons.
Earlier performance archives retain their original source identities and measurements.
