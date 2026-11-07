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

## Second refresh, after the interpreter-free tests

#14362 and #14363 moved eight tests from the Go interpreter to built
drivers and the native CLI. Their old weights were 39 to 574 s, and the
tests now take 16 s or less. The table refresh uses two later runs on main.
In both runs, every self-host job and the `verify` job were green.

- [42348301476](https://github.com/JakeChampion/lang/actions/runs/42348301476),
  commit `45420b1d`, supplies 4,213 measured rows.
- [42350704451](https://github.com/JakeChampion/lang/actions/runs/42350704451),
  commit `06732fe4`, supplies 4,213 measured rows.

The refresh changes 2,076 rows. The replay uses the same method and the same
two runs as above.

| Shards | Weights | Run 42348301476 largest | Run 42350704451 largest |
| --- | --- | ---: | ---: |
| 10 x86 | before | 3,541 s | 4,090 s |
| 10 x86 | after | 3,325 s | 3,817 s |
| 2 aarch64 | before | 16,402 s | 18,467 s |
| 2 aarch64 | after | 16,234 s | 18,237 s |

The mean x86 shard load is 3,223 s in the first run and 3,606 s in the second.
The largest x86 shard goes to 3 to 6 percent above the mean.

The two runs measure the session's CI changes on main. Run 42304783791, before
them, used 828 runner minutes, and its self-host lane used 394. Run
42350704451 used 698 runner minutes, and its self-host lane used 296. The
summed self-host test time went from 39,891 s to 36,060 s. In run 42348301476
it was 32,231 s. Neither run hit the oracle verdict cache, because the Go
checker changed in #14369.
