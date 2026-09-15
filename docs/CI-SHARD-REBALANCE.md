# Self-host shard rebalance candidate

Status: private candidate, awaiting a separate validation run. Do not publish
or adopt this weight table until that validation succeeds.

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

## Frozen candidate and next gate

The uncommented weight rows have SHA256
`bfe63771c9fc2aa14f60ea05241dc2cc539f784c7a5ff7c93b89e4c25ab43efb`.
Validate these rows unchanged against a separate completed run with full
timings and verified test inventory. Report any new or untimed tests instead
of silently dropping them. Do not tune this table using that validation run
and continue calling the same run independent evidence.

The buffered compiler candidate changes execution costs and adds a test.
Revalidate against its resulting inventory and observations before publishing
this later rollout. Preserve all test selection, timeouts and outcome gates.

Local replay driver: `/tmp/lang-ci-replay-two-runs.py`. Fixed rows, assignments,
per-run inventories and report: `/tmp/lang-ci-two-lifetime-replays`.
