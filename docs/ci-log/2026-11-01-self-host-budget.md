# Self-host worker budget recovery

Run [41253378891](https://github.com/JakeChampion/lang/actions/runs/41253378891)
exceeded the unchanged 28-minute worker deadline on x86 shard 0.
Run [41253417762](https://github.com/JakeChampion/lang/actions/runs/41253417762)
also exceeded that deadline on an x86 shard.
Both runs also contain record-drop fixture failures.

The first run supplies 4,139 completed top-level observations from its eight
x86 shard logs. Each observation measures the interval from `RUN` to its
terminal event, without explicit pause intervals. Interrupted tests supply
no duration. The weight update uses `max(existing, ceil(1.5 * seconds))`.
It raises or adds 1,383 rows and decreases none. This is a partial input
from a failed run, so it cannot support lower weights.

The Linux x86 test inventory contains 4,201 selected tests after the current
workflow exclusions. The replay uses the actual `scripts/shard-tests`
partition and the same four-worker assignment as `ci-test-workers`.
The original partition matches all eight logged selection counts and every
started test. Each candidate assigns every selected test exactly once.
Twenty self-host tests and 42 residual tests lack completed observations.
The replay assigns no runtime to those tests.

| Weights | x86 shards | Largest sum of completed observations in a self-host worker |
| --- | ---: | ---: |
| Original | 8 | 1,677.582 s |
| Twenty raises from the audit | 8 | 1,699.996 s |
| All completed observations, raises only | 8 | 1,612.478 s |
| All completed observations, raises only | 9 | 1,475.891 s |

The selected candidate uses nine x86 shards. The outcome verifier requires
all nine markers. The two ARM shards, worker count, deadline, test selection
and assertions stay unchanged. The extra runner supplies capacity for the
observed workload. This partial replay does not establish live wall time,
queue latency or a successful run. Current-head CI must validate the candidate.

Local checks cover the worker scheduler, duration collector, workflow lint,
test names and the current shard-outcome verifier. Local evidence remains
under `/tmp/fern-helper-weights-job-*.json`,
`/tmp/fern-helper-weight-replay.py` and its JSON output. These files contain
the measured inputs and the exact original-selection audit.
