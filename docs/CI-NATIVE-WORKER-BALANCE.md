# Native worker assignment experiment

The two-worker native x86 trial in
[run 35022735930](https://github.com/JakeChampion/lang/actions/runs/35022735930)
split 516 parent tests evenly by count. Worker zero took 602.479 and 615.194
seconds; worker one took 338.259 and 342.588 seconds. The imbalance leaves
one worker idle while the other finishes expensive tests.

The optional `ci-test-workers -weights FILE` assigns tests in descending
measured duration to the least-loaded worker, breaking ties deterministically.
The actual binary inventory remains authoritative. Unlisted tests receive
weight one; stale names cannot introduce tests. Invalid, duplicate, nonpositive
or nonfinite weights fail before starting tests. Without the flag, the original
round-robin assignment is unchanged. CPU division, timeouts and complete
outcome verification remain unchanged.

## Evidence and limits

The experimental table uses only the first two-worker trial. Replaying it
against the second trial changes the longer recorded active-time sum from
614.94 to 485.21 seconds. There are 515 timing rows and one untimed skipped
heap benchmark, which remains assigned with default weight one. Its duration
is not invented or included in the measured sum.

The first native pilot rejected five timing rows rounded to zero. The
experimental table now floors those scheduling weights at 0.01 seconds;
that floor is not a measured duration. A regression check parses the actual
table before publication. The replay above predates this correction and is
only exploratory evidence; live measurements must use the corrected table.

Both trials share a runner and binary. Reassignment can change fixture reuse
and CPU or memory contention. This replay supports a controlled experiment,
not a live speedup claim or production adoption. The weight table is frozen
before native validation; no production workflow enables weighted assignment.

## Validation plan

Parser and assignment tests cover malformed inputs, missing/stale weights,
overflow, deterministic ties, registration order and complete worker groups.
Subprocess tests cover successful tests, failing tests and crashes with the
weighted path. These tests, source policies and Linux vet pass locally.

The dispatch-only experiment compares two unweighted workers, two weighted
workers, two weighted workers, then two unweighted workers. All trials use the
same compiled binaries and CPU allocation. Inventory, every terminal outcome,
skip identity and raw events must match. A sub-minute pilot must pass before
changing only the scale input to run the full native suite. Full current-head
repository CI is also required before any production rollout.
