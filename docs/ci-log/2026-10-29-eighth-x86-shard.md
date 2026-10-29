# An eighth x86-64 self-host shard on October 29, 2026

Run [40807184952](https://github.com/JakeChampion/lang/actions/runs/40807184952)
failed on x86-64 shard 2 at the 28-minute worker deadline.
Three of its four workers ran out of time, and the fourth finished at 1,575 seconds.
Shard 0 finished in 27 minutes and 40 seconds.
Shard 3 of run 40789933955 and shard 3 of run 40748946900 failed the same way.

The shard was full, not skewed.
Shard 2 recorded 514 durations that sum to 6,580 seconds.
Its declared weights sum to 5,730 seconds.
A shard's four workers hold 6,720 seconds, so the declared load was 85 percent of the budget.
Its 18 tests above 60 seconds measured 3,463 seconds against 3,594 declared, so the heavy rows were true.

The gap came from dozens of mid-weight tests, each a few seconds above its row.
No row change moves that gap, because the whole lane was at capacity.

The partition script places the live test list into eight buckets at 5,234 declared seconds each.
Seven buckets held 5,982 seconds each, 89 percent of the budget.
Eight buckets hold 78 percent, under the 85 percent near-budget line of `scripts/ci-test-weights`.
The matrix in `test-e2e-selfhost.yml` has eight x86-64 shards, and the verify job expects eight outcomes.
The worker deadline stays at 28 minutes.
