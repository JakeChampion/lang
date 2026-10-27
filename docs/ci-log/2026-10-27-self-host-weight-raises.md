# Self-host weight increases on October 27, 2026

Run [40506583681](https://github.com/JakeChampion/lang/actions/runs/40506583681)
failed on x86-64 shards 0, 1 and 3 at the 28-minute worker deadline.
Shard 6 also found a stale `ssarc.lower` consumer in `TestSelfHostPackedBytesGraph`.
The fixture now matches the standard result before it reads the body.
Its local regression test passes.

The verify job reports full test lifetimes, with explicit pauses excluded.
Its raises-only audit recommends 84 weight increases or additions.
Each new value is the ceiling of 1.5 times the measured duration.
This change applies those values without decreases or test exclusions.
The worker deadline stays at 28 minutes.

The offline replay uses the actual partition script and worker inventory code.
The inventory contains 4,099 tests from the exact CI merge tree, `3e73589b1`.
The replay reproduces every recorded worker count and completed shard's test membership.
Each candidate test belongs to exactly one shard and one worker.

The largest observed worker sum decreases from 1,743.67 to 1,556.82 seconds.
These sums use the verify job's slowest observation across targets.
The failed run provides 3,931 measured test durations.
Interrupted and unstarted tests have no measured duration.
The replay cannot establish the final worker duration or whole-suite success.
Live CI must confirm the new balance.

The task workspace retains all seven x86-64 shard logs and the verify log.
It also retains the selected inventory, duration rows, replay code and assignment records.

## Live validation

[Run 40528941443](https://github.com/JakeChampion/lang/actions/runs/40528941443)
completed successfully for head `422c4124f` with the new weights and the unchanged deadline.
All seven x86-64 shards, both ARM64 shards, the CLI driver job, and the verify job passed.
Later commits require their own CI results.
