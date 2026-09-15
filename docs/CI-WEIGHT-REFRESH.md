# Refreshing measured shard weights

The live weight audit deliberately only raises weights from one run. That
protects against a single unusually fast sample, but cannot account for
compiler and test improvements. Stale high weights also unbalance shards.

`scripts/ci-test-weights refresh` produces a replacement weight table from
multiple comparable, successful CI runs. It uses the slowest observation,
rounded up to whole seconds, with a minimum of one:

- Two distinct runs must observe a test before its weight can decrease.
- One observation can increase a weight or establish a new slow test.
- Repeated rows or retry artifacts within one run count as one observation.
- Unobserved weights remain unchanged. Weight-one entries are omitted because
  the partitioner already defaults to one.
- Missing, empty, repeated-directory and malformed input is rejected.

The command writes to stdout and does not edit the repository. The existing
single-run `check` behavior and the live test-list partition are unchanged.

## Procedure

Choose at least two successful runs with comparable runner types, toolchains,
cache warmth and source workload. Confirm their success from CI: timing rows
alone do not prove it. Download each run's complete shard timing artifacts to
its own directory, with the `*.timings` files directly inside that directory.

```sh
scripts/ci-test-weights refresh .github/selfhost-test-weights.txt \
  /tmp/run-one-timings /tmp/run-two-timings > /tmp/candidate-weights.txt
```

Inspect the differences and preserve useful provenance comments when updating
the checked-in file. Do not redirect output onto the input file: the shell
would truncate it before the script could read it.

Before applying a candidate, replay the actual partition against a separate
completed run. Use its full selected test list, including non-`TestSelfHost`
tests from the self-host package. First reproduce the existing assignments
exactly. Then check that the candidate assigns every test once, and sum that
run's observed durations for each new shard. This is an offline comparison;
confirm the resulting balance and queue latency in live CI after rollout.

## Evidence motivating the refresh

All comparisons below use the actual partition script, six shards, native
Linux x86-64 CI durations, and an independent run for evaluation. Existing
assignments were reproduced exactly before evaluating alternatives.

| Weight source | Evaluation run | Tests | Existing longest test-work sum | Replayed longest sum |
| --- | --- | ---: | ---: | ---: |
| Run 34979953900, rounded-up durations | [34983823915](https://github.com/JakeChampion/lang/actions/runs/34983823915) | 2590 | 1282.24 s | 1100.51 s |
| `refresh` from 34979953900 and 34983823915 | [34983091167](https://github.com/JakeChampion/lang/actions/runs/34983091167) | 2587 | 912.79 s | 870.41 s |

These represent 14.17% and 4.64% reductions in replayed maximum test work.
They are not measured whole-CI improvements. The second evaluation includes
the source-staging optimization, so it also tests the candidate against a
changed workload. The checked-in weights have not been replaced by this tool
change: refresh their inputs after the pending scheduling changes land.

The existing tests plus refresh regressions cover increases and decreases,
missing observations, retries within one run, order independence, fractional
unobserved weights, malformed rows and duplicate run directories. They run
through the actual Bash/AWK implementation on macOS and Linux.
