# CI performance investigation

## Baseline: September 15, 2026

The first bottleneck is stale main validation waiting behind older commits.
An API snapshot near 12:55 UTC contained 34 pending main pushes among the
latest 100 CI runs, alongside 62 cancelled runs and four successful runs.
This is a bounded sample, not an inventory of the entire backlog.

| Run | Event | Created (UTC) | First runner job (UTC) | Queue delay (seconds) | Job execution span (seconds) | Sum of runner job wall times (seconds) |
| --- | --- | --- | --- | ---: | ---: | ---: |
| [34936726276](https://github.com/JakeChampion/lang/actions/runs/34936726276) | PR | Sept 15 06:23:35 | Sept 15 06:58:56 | 2121 | 2251 | 19527 |
| [34934066938](https://github.com/JakeChampion/lang/actions/runs/34934066938) | PR | Sept 15 05:45:53 | Sept 15 06:18:29 | 1956 | 2425 | 19250 |
| [34900722213](https://github.com/JakeChampion/lang/actions/runs/34900722213) | main push | Sept 14 21:47:21 | Sept 15 12:24:08 | 52607 | Still running at snapshot | Incomplete |

The PR samples completed successfully with substantial test coverage. Their
six x86 selfhost shards took 1051-1401 and 951-1514 seconds per job,
respectively. Job time includes setup and build time, not just tests.
The runner sums measure allocated wall time, not CPU utilization or billing.
CPU, memory, cache behavior and individual test timings need further measurement.

Method: read workflow runs through the Actions API, then paginate each run's
jobs. Queue delay is the earliest actual runner job `started_at` minus the
run's `created_at`. Execution span ends at the last job `completed_at`.
Sum completed jobs with a nonzero `runner_id` for allocated runner wall time.
Do not use `run_started_at` as the execution boundary: observed pending
runs reported their creation time there. Exclude docs-only runs when comparing
full-suite execution times and compare equivalent targets and runner classes.

## First change: coalesce pending main validation

Main uses a separate entry workflow, `ci-main.yml`, which calls the shared
`ci.yml`. It preserves the active suite and uses GitHub's default single
pending slot. A new push replaces an older pending push. PRs retain their
existing FIFO queue, filters and check names; manual CI dispatches remain
independent. The nested CI lock has a separate per-run name to avoid waiting
on its own caller.

Every selected main commit runs all lanes. Filtering only the latest push's
diff would be incorrect: a code push followed by a docs-only push could lose
code coverage when the intermediate run is replaced. Running full coverage
also means an isolated docs-only main push costs a full suite. The policy
trades per-commit attribution for fresher validation of the combined tree.

The main failure observer watches both workflow names during migration and
removes the new `Validate / ` prefix before identifying lanes. Existing
failure issues and Bootstrap guidance retain their identities. A cancelled
pending run has no completed jobs and cannot close a failure issue.

GitHub documents the [single pending slot and active-run protection](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency),
and [nested workflow permissions and concurrency](https://docs.github.com/en/actions/reference/workflows-and-actions/reusing-workflow-configurations).

Validation includes execution of the real change-filter and failure-reporter
scripts against stubbed APIs, workflow source checks, and actionlint. Actual
post-merge runs must confirm pending replacement, active completion, full
lane execution, failure reporting and queue latency before claiming a speedup.
The old FIFO belongs to runs created from the previous workflow revision;
changing the entry does not drain it. Inspect that backlog after the new
full-coverage main run is established, preserving active validation during
any one-time cleanup.

## Next measurements

Separate setup/build/test time on the critical selfhost shards, identify
duplicated builds, and measure available CPU use before increasing concurrency.
Profile slow tests and generated programs before changing the compiler.
Keep meaningful target and semantic coverage, and compare observed before/after
runs rather than extrapolating a numeric speedup from the queue policy alone.
