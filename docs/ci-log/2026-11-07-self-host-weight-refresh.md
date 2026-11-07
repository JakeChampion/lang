# Self-host shard weights after the cached CLI

Status: [record] measurements of 2026-11-07 and the change they drove.

The cached self-host CLI (#14343) removed a driver build from many tests.
The refresh changes 2,090 rows of `.github/selfhost-test-weights.txt`. In 148 of
them, one weight is 10 s or more. In each of the 148, the new weight is less
than half or more than double the old weight.

The replacement table uses `scripts/ci-test-weights refresh` and two runs on
main. In both runs, every self-host job and the `verify` job were green. Run
42294630348 failed only in the Showcase browser job (#14350). Each run directory holds
the rows of its `verify` job's "measured durations" group.

- [42294630348](https://github.com/JakeChampion/lang/actions/runs/42294630348),
  commit `17e6932c`, supplies 4,207 measured rows.
- [42304783791](https://github.com/JakeChampion/lang/actions/runs/42304783791),
  commit `075aa763`, supplies 4,207 measured rows.

The offline replay partitions all 4,207 tests with `scripts/shard-tests`. It
then sums each run's measured durations for each shard. Each test goes to
exactly one shard.

| Shards | Weights | Run 42294630348 largest | Run 42304783791 largest |
| --- | --- | ---: | ---: |
| 10 x86 | before | 4,462 s | 4,576 s |
| 10 x86 | after | 4,072 s | 4,136 s |
| 2 aarch64 | before | 19,644 s | 19,993 s |
| 2 aarch64 | after | 19,682 s | 20,133 s |

The mean x86 shard load is 3,905 s. The largest x86 shard goes from 14 to
17 percent above the mean to 4 to 6 percent above it. The aarch64 split
changes by less than one percent.

The replay uses the same two runs as the refresh. No third run with
#14343 and a green self-host lane existed. Live CI on main must therefore confirm the balance.

The shard counts, the four workers, the deadlines and the test filters stay
unchanged.
