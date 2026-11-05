# Self-host runner resources and shard balance

CI run [42025432548](https://github.com/JakeChampion/lang/actions/runs/42025432548)
failed on two self-host shards. Shard 0 reported no space left for three
test files. Its checksum worker later stopped without complete test JSON.
Shard 3 reached the 28-minute worker deadline. The test in progress had run
for 27 seconds, and the next test had no outcome.

## Child crash protection

The shared x86 and ARM64 program launchers bypassed `WithoutCoreDump`.
That helper disables QEMU guest cores and clears the Linux core filter.
The filter also matters when the host has a piped core handler.

The launchers now use the existing helper. The original regression covers
both native paths and both runner paths. Before the change, all four
paths failed the cleared-filter check. After it, the complete harness and
worker-runner suites pass. The test still requires SIGSEGV, the original
argument bytes, and unchanged parent limits and filter.

The Linux CI worker launcher also clears both controls before execution.
Its descendants inherit them, including direct Wasmtime and checksum
commands. A separate regression checks a real descendant, the argument
bytes, SIGSEGV and parent isolation. The process-group cancellation test
still requires the worker to kill descendants without an open output pipe.

The CI logs do not measure the files that filled the disk. This repair
closes a verified gap in crash protection. Fresh CI must confirm the
resource result.

## Measured weights

The replacement table uses the existing `ci-test-weights refresh` command
and two successful runs with successful verification jobs:

- [42010207208](https://github.com/JakeChampion/lang/actions/runs/42010207208),
  commit `aa70b1dc`, supplies 4,179 measured rows.
- [42008381307](https://github.com/JakeChampion/lang/actions/runs/42008381307),
  commit `d42b0a30`, supplies 4,180 measured rows.

Each row rounds up the slower observation. A decrease requires both runs.
An unobserved row retains its old weight. One-second rows use the existing
default.

An independent replay uses all 4,289 Linux x86-64 tests from green run
[41985402573](https://github.com/JakeChampion/lang/actions/runs/41985402573),
commit `cf6c698d`. Its ten shard logs supply all 4,165 passes and 124 skips.
The replay reproduces every original shard and worker assignment. The
weight file on that commit exactly matches the file before this repair.

With the refreshed weights, the largest sum of observed test durations
changes from 1,498.47 to 1,241.25 seconds. These are offline sums of measured
durations, not new measurements of live CI time. The current inventory has
4,290 tests. Its additional worker-child test has a measurement from the
second training run and retains its place in the live selection.

The ten x86 shards, four workers, deadlines, test filters and performance
baselines stay unchanged. Every live test still has exactly one shard.
Fresh CI must pass every required check before merge.
