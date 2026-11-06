# Self-host CLI builds through the driver cache

Status: [record] measurements of 2026-11-06 and the change they drove.

## What the full suite was waiting on

Two green full-suite runs on main were read with the jobs API:
[42267280766](https://github.com/JakeChampion/lang/actions/runs/42267280766)
and [42256240610](https://github.com/JakeChampion/lang/actions/runs/42256240610).

| Measure | 42267280766 | 42256240610 |
| --- | ---: | ---: |
| First job start to last job end | 74 min | 70 min |
| Runner minutes | 879 | 882 |
| Longest self-host x86-64 shard | 25 min | 25 min |
| Longest wait of a self-host shard for a runner | 41 min | 36 min |
| Slowest shard, summed test seconds | 5,412 | 5,155 |
| Fastest shard, summed test seconds | 3,400 | 3,435 |

The self-host x86-64 shards end each run. Half of them wait 25 to 41 minutes
for a runner, because other workflows hold the shared pool. The shard
partition costs 3 to 4 minutes of that time. The waits cost ten times more.

## Where the shard imbalance came from

The weight audit of the `verify` job flagged 31 and 32 tests. 30 tests
measured near 0.1 seconds in one run and 60 to 120 seconds in the other. The
other flagged tests measured 60 to 178 seconds in both runs, against weights
of 1 to 4.

Most of the flagged tests compiled `compiler/fern.fern` before they ran:

- `selfHostCLIForHost` and `witSelfHostCLI` compiled it once in each worker
  process, through `cmd/fern` and with no cache. The first caller in each
  worker paid the build.
- 14 socket, HTTP and reactor helpers compiled it again in each call.
- `drivers/runtime_run.fern` and `drivers/interp_run.fern` used the driver
  cache, but `driver-sizes` did not build them. Each worker that needed one
  built it.

A build takes 60 to 100 seconds on a CI runner. The ten x86-64 shards run
four workers each, so a run paid for up to 40 builds of each driver. Which
test paid depended on the order of the tests in a worker. A weight cannot
describe that cost.

## The change

All of these helpers now get the CLI from `buildSelfHostBinFor`. That path
builds the driver with the stage0 pin and keys it by its source closure. The
build goes into the directory that the Actions cache restores. `driver-sizes` builds `fern.fern`
already, so the x86-64 shards restore it. `drivers/runtime_run.fern` and
`drivers/interp_run.fern` are now rows in `.github/selfhost-driver-sizes.txt`,
so `driver-sizes` builds and measures them too.

Measured on the 4-core dev container:

| Test | Cold cache | Warm cache |
| --- | ---: | ---: |
| `TestSelfHostInlineShortCircuitLeaf` | 69.7 s | 0.03 s |
| `TestSelfHostAttributeGrammar` | 158 s (CI, before) | 3.2 s |
| `TestSelfHostNativeSocketSendSuppressesSIGPIPE` | 57 s (macOS CI, before) | 0.4 s (Linux) |

`driver-sizes` takes about 75 more seconds when the two new drivers miss the
cache.

The macOS self-host step ran 12 tests of 55 to 75 seconds each. Most of that
time was the same CLI build. Because of that step, macOS shard 0 ran 37 to 41
minutes and shard 1 ran 25 to 27 minutes. No step moves between the
two shards in this change.

## Weights

The weights stay as they are in this change. The two runs above measured the
CLI builds that this change removes. A refresh from them raises the weights
of the tests that paid a build. Refresh the table from two green
runs after this change, by the procedure in `docs/CI-WEIGHT-REFRESH.md`.
