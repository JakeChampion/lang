# Rebalance and parallelize self-host test shards

Status: successful held-out replays and a successful live comparison with
identical test binaries. Current-head checks remain required before merge.

The old weights include terminal test durations that omit parallel children.
The corrected collector measures each top-level test's full active lifetime.
This candidate uses the slower observation from two completed successful
native x86 runs, rounded up to a whole second. Lowering requires observations
in both runs; unobserved weights remain unchanged. Missing entries weigh one.

Training runs:

- [35028280986](https://github.com/JakeChampion/lang/actions/runs/35028280986),
  source `b975834b4a2fa242bdd41a2014f6a0fb30d0e693`.
- [35032237412](https://github.com/JakeChampion/lang/actions/runs/35032237412),
  source `f6aeba4eaf3ee6eda5ba9920acd0580469d0eb4c`.

Both selected exactly 2,601 tests. Actual test binaries were hash-checked and
their registration tables decoded to recover the complete inventory. The
workflow exclusions and original partition were read from each run's source.
Every timing row matched its original shard; all selected tests were assigned
exactly once. Both runs have 2,595 timing rows and the same six untimed tests.
No duration is invented for those six tests.

### Entries that now use the one-second fallback

The refresh omits explicit entries whose rounded replacement is one second.
This lowers 26 previous weights to the existing fallback, rather than retaining
their old values. Every such test was observed in **both** training runs and
its slower full active lifetime was at most one second. None was unobserved
or removed from selection. These are the complete omissions and their inputs:

| Test | Previous weight | Run 35028280986 seconds | Run 35032237412 seconds |
| --- | ---: | ---: | ---: |
| `TestSelfHostAppendValueSemanticsWasmIR` | 45 | 0.55 | 0.43 |
| `TestSelfHostAppendValueSemanticsX86_64` | 45 | 0.53 | 0.43 |
| `TestSelfHostArm64ProcExecRuns` | 15 | 0.62 | 0.65 |
| `TestSelfHostAsyncCombinatorsModloadIRX86_64` | 12 | 0.84 | 0.66 |
| `TestSelfHostBigI64LiteralWasmIR` | 12 | 0.48 | 0.29 |
| `TestSelfHostCellWasmIR` | 6 | 0.94 | 0.99 |
| `TestSelfHostExportAttributeCompiles` | 6 | 0.55 | 0.29 |
| `TestSelfHostFnArrayFieldGateWasm` | 12 | 0.62 | 0.37 |
| `TestSelfHostFnArrayFieldGateX86_64` | 12 | 0.41 | 0.44 |
| `TestSelfHostFnValueCaptureIRX86_64` | 35 | 0.78 | 0.47 |
| `TestSelfHostFnValueCaptureWasmIR` | 35 | 0.58 | 0.62 |
| `TestSelfHostFormatStringIRWasm` | 12 | 0.74 | 0.59 |
| `TestSelfHostReadAllStdinIRArm64` | 20 | 0.83 | 0.45 |
| `TestSelfHostReadAllStdinIRRoutingX86_64` | 6 | 0.68 | 0.62 |
| `TestSelfHostReadAllStdinIRWasm` | 12 | 0.61 | 0.39 |
| `TestSelfHostReadAllStdinIRX86_64` | 10 | 0.75 | 0.82 |
| `TestSelfHostSSALiftCoverageScan` | 6 | 0.37 | 0.25 |
| `TestSelfHostSaturatingWasmIR` | 25 | 0.92 | 0.99 |
| `TestSelfHostSaturatingX86IR` | 25 | 0.84 | 0.84 |
| `TestSelfHostTupleFnZeroArgIRWasm` | 12 | 0.75 | 0.71 |
| `TestSelfHostTupleFnZeroArgIRX86_64` | 12 | 0.66 | 0.50 |
| `TestSelfHostWasmArityGate` | 10 | 0.65 | 0.50 |
| `TestSelfHostWasmComponent` | 4 | 0.86 | 0.50 |
| `TestSelfHostWasmComponentAdapter` | 30 | 0.96 | 0.86 |
| `TestSelfHostWitClassify` | 5 | 0.79 | 0.48 |
| `TestSelfHostWitPrefixLayout` | 5 | 0.88 | 0.70 |

Stale weights still matter: assigning an unexpectedly expensive test weight
one can concentrate work on a shard. Prefer a conservative estimate when a
new test has no measurements; relative ordering matters more than precision.
Never lower a measured weight from a single warm-cache observation. Use
[`scripts/ci-test-weights`](../scripts/ci-test-weights) to audit subsequent
runs and refresh from multiple completed runs, including fixture recovery.
The audit's 1.5-times safety band flags substantial underestimates without
treating small timing fluctuations as new evidence. A warm driver becoming
unavailable can invalidate the table even when no test source changes.

## Replay of training observations

| Run | Original longest active-time sum | Candidate longest active-time sum | Original spread | Candidate spread |
| --- | ---: | ---: | ---: | ---: |
| 35028280986 | 918.92 s | 788.13 s | 244.18 s | 20.34 s |
| 35032237412 | 704.03 s | 625.14 s | 205.19 s | 41.92 s |

These are sums of recorded active durations under different assignments, not
measured execution of the candidate or a whole-CI forecast. Both observations
were used to choose the table. Reassignment can change fixture-cache behavior,
so even a successful separate-run replay needs live CI validation.

The self-host test binary is identical between the two training runs. The
residual e2e binary changed after Go x86 SSA handle work; its selected self-host
inventory did not. Retaining the slower observation avoids assuming the
smaller second-run measurements are permanent improvements.

## Independent validation

The frozen table was replayed against successful main run
[35033613487](https://github.com/JakeChampion/lang/actions/runs/35033613487),
source `0cc75a00464132943b50da347ffc95667728c3f5`. This run was not used to
choose or adjust the weights. Its actual binaries were hash-checked and their
registration tables decoded. All 2,601 selected tests were assigned exactly
once, all 2,595 observations matched the original shard, and the same six
tests remained untimed. No tests were added or removed from the inventory.

| Assignment | Longest recorded active-time sum | Spread |
| --- | ---: | ---: |
| Original | 906.74 s | 329.35 s |
| Frozen candidate | 823.02 s | 73.51 s |

This supports the balancing strategy on independent observations. It does
not establish a live speedup: the observations were collected under the old
assignments, and changing assignments may change fixture reuse and overlap.
No production weights have changed.

Reproduction: `/tmp/lang-ci-replay-heldout.py`; downloaded binaries, timing
artifacts, recovered inventory and report:
`/tmp/lang-ci-heldout-35033613487`.

### Buffered compiler validation

The same frozen table was independently replayed against successful PR run
[35036653947](https://github.com/JakeChampion/lang/actions/runs/35036653947),
source `a6e4ca82aeb4e8fe335223e4a5dba55fbacd27f1`, now merged in #9411.
The replay requires an authoritative successful run and matching revision.
Its hash-checked binaries contain 2,602 selected tests, including the new
`TestSelfHostOpBufferSnapshots`, with no removed tests. All 2,596 observations
match their original shard and the same six tests remain untimed.

| Assignment | Longest recorded active-time sum | Spread |
| --- | ---: | ---: |
| Original | 860.38 s | 403.58 s |
| Frozen candidate | 743.02 s | 73.20 s |

The new test uses the existing one-second scheduling fallback. No measured
duration or weight was invented for it. Every selected test remains assigned
exactly once. Artifacts and report:
`/tmp/lang-ci-heldout-buffer-35036653947`.

These results support the candidate after the compiler change, but remain
recorded-duration replays rather than live execution of the new assignment.

## Expand to twelve x86 shards

The candidate also increases the x86 shard count from six to twelve. Each
runner keeps its existing CPU allocation, memory limits and timeouts. The ARM
lane remains one shard. The final outcome verifier requires all twelve x86
markers; the existing source-policy test checks that its counts exactly match
the matrix, including every shard index and partition denominator.

The same frozen weights and 2,602-test inventory were replayed at increasing
shard counts against run 35036653947. Every test remained assigned exactly once,
all 2,596 timed observations were retained, and the same six tests remained
untimed. Only the partition count changed. Repeating the six-shard replay
reproduced the earlier report exactly.

| X86 shards | Longest recorded active-time sum | Spread |
| ---: | ---: | ---: |
| 6 | 743.02 s | 73.20 s |
| 8 | 588.30 s | 109.66 s |
| 10 | 475.63 s | 98.64 s |
| 12 | 411.89 s | 111.23 s |

These observations support trying twelve shards; they are not live timing
claims. The count was selected using this replay, so it is not an independent
validation of that count. More runners add setup and artifact transfer work,
can duplicate fixture builds, and may increase queue pressure. The old
six-shard choice reflected longer individual tests and saturated admission.
Live CI must now measure last-shard completion, queue and setup time, total
job time, fixture reuse, and unchanged test coverage before accepting the
larger matrix. Do not use the work sums as a full-workflow forecast.

### Acceptance under runner contention

The historical warning remains relevant. In
[run 33535490171](https://github.com/JakeChampion/lang/actions/runs/33535490171),
sixteen shards waited 13-37 minutes and admission spread across 24 minutes.
The old six-shard choice was based on that admission behavior and an older
single-test bottleneck. It is not evidence that today's pool can admit twelve
shards promptly.

For this combined rollout, compare the twelve-shard PR against the concurrent
six-shard main run with identical compiled test binaries. Accept it only after
verifying unchanged selected tests and outcome/skip identities, and a shorter
time from completion of all required warm jobs to the last shard's completion.
Report admission spread and per-shard waiting time separately from setup and
test execution, plus aggregate job wall time and fixture-download failures.
Longer aggregate work must be disclosed even if latency improves. Reject or
reduce the larger matrix if admission erases the measured latency benefit.

This measures the **combined** table/count change; it cannot attribute a gain
to either change independently. Overlapping main and PR suites exercise a
contended pool but cannot reproduce every saturation pattern, especially the
historical 24-minute admission spread. Continue monitoring these same metrics
after rollout; a favorable single observation is not a permanent capacity
guarantee. A separate six-shard run with the new weights would be needed to
isolate the effect of shard count.

### Live comparison

Both [six-shard main run 35042377389](https://github.com/JakeChampion/lang/actions/runs/35042377389)
at `d2d44bdb9caf2c40872581a1044c001a9b259239` and
[twelve-shard PR run 35042436488](https://github.com/JakeChampion/lang/actions/runs/35042436488)
at `566f5b4f71b47c277749bbba7195a2860af26b0d` completed successfully.
Their suites overlapped. The actual downloaded test binaries were identical:

- `e2eselfhost.test`: `edf12d2eb98b0a3d6bd3bf8ab59b77fbd12a32787c6050c3d8da3d03d5e3dd07`
- `e2e.test`: `57e80166e00d0c4bef525d2ef39720f333c1d98cf23c88f1679b2079fa4bdae3`

Independent verification recovered the selected inventory from those binaries,
reproduced every shard assignment using its revision's weights, and checked
every logged test start and terminal outcome. Both runs selected 2,602 parent
tests and produced the same 27,208 outcome identities, including identical
skip identities. Both have 2,596 full-lifetime observations and the same six
untimed parents, all explicitly skipped. No test was dropped to get faster.

| Observed metric | Six shards, old weights | Twelve shards, frozen weights |
| --- | ---: | ---: |
| Required warm jobs complete to last shard complete | 1,147 s | 614 s |
| Run created to last shard complete | 1,245 s | 907 s |
| Longest test step | 884 s | 470 s |
| Shard admission wait after warm jobs | 137-239 s | 25-201 s |
| Admission spread | 102 s | 176 s |
| Aggregate setup before test steps | 156 s | 328 s |
| Aggregate test-step wall time | 4,692 s | 4,685 s |
| Aggregate shard-job wall time | 4,866 s | 5,052 s |

The combined change finished 533 seconds sooner after warm-up, or 338 seconds
sooner measured from run creation. The different warm-up/admission schedules
explain why these are different gains. Aggregate job time increased by
186 seconds; the extra jobs added setup work while total test-step time was
nearly unchanged. These are separate-run observations, not a controlled
estimate, CPU consumption, billed cost, or guaranteed whole-CI improvement.

Every test-binary and warm-driver artifact download succeeded in both runs.
The harness does not log individual driver-cache hits, so download success
does not establish a cache-hit rate or count every duplicate fixture build.
The measured test-step totals include any such work. Admission spread grew,
but waiting did not erase the lane's latency benefit. This meets the stated
rollout criterion for the combined change under the observed contention;
it does not prove twelve remains preferable under the historical saturation.

Local audit: `/tmp/lang-ci-audit-live-shards.py`, with downloaded binaries,
outcome artifacts, full job logs and reports under
`/tmp/lang-ci-shard-live-{35042377389,35042436488}`. These local paths record
the analysis workspace, not files shipped in this repository. Public source
inputs are the run links above, their `warm-test-binaries-x86_64` and
`shard-outcome-*` artifacts, and the revision's
[`scripts/shard-tests`](../scripts/shard-tests) and
[`scripts/ci-test-weights`](../scripts/ci-test-weights). Artifacts have limited
retention; the table records the verified observations. The refresh procedure
is documented in [CI-WEIGHT-REFRESH.md](CI-WEIGHT-REFRESH.md).

Reports: `/tmp/lang-ci-heldout-buffer-35036653947/report-shards-{6,8,10,12}.json`.
Reproduction adds `--candidate-shards N --report-name report-shards-N.json` to
the held-out replay command; it does not alter the frozen weight rows.

## Frozen candidate and next gate

The uncommented weight rows have SHA256
`bfe63771c9fc2aa14f60ea05241dc2cc539f784c7a5ff7c93b89e4c25ab43efb`.
These rows remained unchanged during independent validation. Report any new
or untimed tests in subsequent validation instead of silently dropping them.
Do not tune this table using the validation run and continue calling that
same run independent evidence.

The buffered compiler's inventory, live outcomes and admission behavior have
now been validated. Preserve all test selection, timeouts and outcome gates,
require successful current-head CI before merge, and keep reporting actual
shard execution and fixture behavior separately from replayed work sums.

Local replay driver: `/tmp/lang-ci-replay-two-runs.py`. Fixed rows, assignments,
per-run inventories and report: `/tmp/lang-ci-two-lifetime-replays`.
