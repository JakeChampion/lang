# Self-host shard rebalance candidate

Status: private candidate with successful held-out replays before and after
the buffered compiler rollout. Live validation remains required.

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

## Frozen candidate and next gate

The uncommented weight rows have SHA256
`bfe63771c9fc2aa14f60ea05241dc2cc539f784c7a5ff7c93b89e4c25ab43efb`.
These rows remained unchanged during independent validation. Report any new
or untimed tests in subsequent validation instead of silently dropping them.
Do not tune this table using the validation run and continue calling that
same run independent evidence.

The buffered compiler's inventory and observations have now been validated.
The next gate is successful live CI with the frozen assignment. Preserve all
test selection, timeouts and outcome gates, and report actual shard execution
and fixture behavior separately from the replay's predicted work sums.

Local replay driver: `/tmp/lang-ci-replay-two-runs.py`. Fixed rows, assignments,
per-run inventories and report: `/tmp/lang-ci-two-lifetime-replays`.
