# Self-host shard balance, 2026-11-03

Status: offline replay passes. Live CI must confirm the new schedule.

## Failures

[Main run 41663582561](https://github.com/JakeChampion/lang/actions/runs/41663582561)
exhausted three workers in x86 shard 4.
[Walkie run 41668392373](https://github.com/JakeChampion/lang/actions/runs/41668392373)
exhausted all four workers in x86 shard 2.
Each worker retained its 28-minute limit. Interrupted tests supplied no duration.

[Socket run 41669959339](https://github.com/JakeChampion/lang/actions/runs/41669959339)
passed every test in all nine x86 shards. Shard 0 reported cancellation after
30 minutes and five seconds, although every step reported success or a skip.
Its longest worker took 1,674.231 seconds. Setup, residual tests, artifacts and
cleanup consumed the rest of the outer job budget.

## Schedule

The weight table uses completed x86 observations from the main and Walkie runs.
For each observed test, the new weight is
`max(existing weight, ceil(1.5 * slowest observed seconds))`.
This changes 1,298 rows. No weight decreases. Each interrupted test supplies no
duration from that run. A test with no complete observation retains its weight.
These weights assign work. They do not set a performance acceptance limit.
The shared table excludes three new Walkie tests that Socket does not define.

The x86 matrix expands from nine shards to ten. Each shard retains four
isolated workers, one CPU per worker, and the same test selection rules.
The ARM matrix retains two shards. The worker limit remains 28 minutes.
The outer job limit becomes 35 minutes, as in the existing stdlib checker job
with a 28-minute test limit. The vanished-shard classifier uses the same limit.
All outcome checks and failure propagation remain intact.

## Replay

The replay first reproduces all nine original assignments exactly. The live
test inventory includes skipped tests, which duration artifacts omit. Omitting
those names changes the partition and invalidates a replay.

The Socket run supplies an independent, complete test workload: 4,254 selected
tests. Its logs identify all selected names and all skipped names. The replay
assigns each name exactly once with the actual `scripts/shard-tests` script.
It then applies the worker partition separately to each package, as CI does.
Skipped tests retain their assignment weight and contribute zero measured work.

| Socket replay | Nine original shards | Ten revised shards |
| --- | ---: | ---: |
| Longest worker, seconds | 1,672.17 | 1,492.12 |
| Longest shard, normal plus residual workers, seconds | 1,676.92 | 1,497.58 |

These are sums of observed test lifetimes. They exclude process startup and
cleanup. They do not predict queue latency or establish a live CI speedup.
Main and Walkie contain interrupted tests, so their partial replays cannot
establish a complete execution budget. Live CI remains the acceptance gate.

An initial replay omitted skipped names and failed assignment reproduction.
A main-only weight candidate also worsened Walkie's observed partial load.
Both candidates were rejected. The final candidate uses the two stated input
runs and keeps the independent Socket observations separate.
