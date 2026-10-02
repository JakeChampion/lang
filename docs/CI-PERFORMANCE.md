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


## Second measurement: September 21, 2026 — the queue, and what bounds it

100 `ci.yml` pull-request runs over 19.5 h, 25 `ci-main.yml` runs over 14.6 h,
and job-level timings for ten complete suites. Method as above: queue delay is
the earliest `Full suite` job `started_at` minus the run's `created_at`; skipped
jobs and jobs with no `runner_id` are excluded; macOS jobs are counted apart
because they draw on a separate, much smaller pool.

One full suite is 61-65 Ubuntu runner jobs plus one macOS job, ~330 Ubuntu
job-minutes, and executes in 16.6-24.0 minutes (median 20.6). It peaks at 36-38
concurrent Ubuntu jobs but averages only ~20 across that span, because the
fan-out ramps and then decays.

| Run | Event | Queue delay (min) | Suite execution (min) | Ubuntu jobs | Ubuntu job-minutes |
| --- | --- | ---: | ---: | ---: | ---: |
| [35564879012](https://github.com/JakeChampion/lang/actions/runs/35564879012) | main | 0.1 | 16.7 | 65 | 322 |
| [35566511671](https://github.com/JakeChampion/lang/actions/runs/35566511671) | main | 0.2 | 16.6 | 65 | 313 |
| [35606626750](https://github.com/JakeChampion/lang/actions/runs/35606626750) | PR | 24.1 | 18.4 | 61 | 329 |
| [35613806204](https://github.com/JakeChampion/lang/actions/runs/35613806204) | PR | 18.3 | 19.9 | 64 | 327 |
| [35622498890](https://github.com/JakeChampion/lang/actions/runs/35622498890) | PR | 11.8 | 22.2 | 64 | 351 |
| [35624238793](https://github.com/JakeChampion/lang/actions/runs/35624238793) | PR | 18.5 | 20.6 | 64 | 326 |
| [35626267621](https://github.com/JakeChampion/lang/actions/runs/35626267621) | main | 0.5 | 24.0 | 65 | 352 |
| [35626378245](https://github.com/JakeChampion/lang/actions/runs/35626378245) | PR | 19.1 | 21.6 | 61 | 327 |
| [35634344508](https://github.com/JakeChampion/lang/actions/runs/35634344508) | PR | 0.5 | 23.2 | 64 | 346 |
| [35638612903](https://github.com/JakeChampion/lang/actions/runs/35638612903) | PR | 6.8 | 20.7 | 64 | 342 |

A PR spent about as long waiting as testing: median queue delay 18.3 minutes
against a 20.6-minute suite, and 0.5 minutes only when it found the queue empty.
End to end, successful PR runs ran 18.6 minutes at best, median 32.8, p90 49.7,
maximum 72.5. The queue was contended most of the time: 2 or more PR runs were
alive for 62% of the sampled minutes, 3 or more for 22%, at most 5.

### The ceiling is ~37 concurrent Ubuntu jobs, and it is the real constraint

The repository is public, so standard runners cost nothing and minutes are not
the scarce resource. Concurrent job slots are. A main suite running alone gets
its 38 jobs picked up almost immediately — median wait 0.07 min, maximum 1.35.
Main runs outside the PR queue, so one of them overlapping a PR suite measures
what two concurrent suites actually do. Per minute, jobs running in each and
jobs queued in each:

| Time (UTC) | PR running | main running | Sum | PR queued | main queued |
| --- | ---: | ---: | ---: | ---: | ---: |
| 16:33 | 37 | 0 | 37 | 2 | 45 |
| 16:34 | 29 | 7 | 36 | 16 | 38 |
| 16:37 | 14 | 22 | 36 | 15 | 15 |
| 16:41 | 17 | 20 | 37 | 7 | 0 |
| 16:45 | 16 | 21 | 37 | 0 | 4 |
| 16:47 | 12 | 19 | 31 | 0 | 0 |

The sum held flat at 35-37 for fourteen minutes while BOTH runs had jobs
queued. That is an account-wide ceiling, not a coincidence of two ramps, and it
matches the 40 concurrent standard jobs a Pro account allows once lint, the
reapers and `check-sources` are also drawing on it. It has not been confirmed
against the billing settings; the number here is inferred from queueing
behaviour alone.

Both suites still finished inside 25 minutes — against ~41 serialized — at the
cost of stretching the main suite from its solo 16.6 to 24.0 minutes. Day-level
pool utilisation is only ~40-50%.

### Why the queue widened to two, and no further

Serializing pull requests did not conserve the scarce resource; it idled it in
every suite's ramp and tail while the next PR waited 18 minutes. The queue was
also only half enforced: main bypasses it, and at ~1.7 main runs an hour of ~23
minutes each, a main suite is live roughly 65% of the wall clock, so a PR rarely
held the pool alone anyway.

Two lanes (`pr-ci-even` / `pr-ci-odd`, split on the parity of the PR number)
fill that slack. Expected effect: queue delay ~18 down to ~3-5 minutes, suite
execution 20.6 up to ~25-27, net PR round ~39 down to ~30.

A third lane was rejected. Once the ceiling is the binding constraint, more
admitted suites add no throughput — two already pinned the pool for fourteen
straight minutes — and the observed queue depth of 5 would put five suites'
325 jobs against ~37 slots, stretching every tail and losing FIFO fairness: a
docs-only PR would sit behind four compiler suites.

The queue policy is not where the remaining win is. 330 job-minutes packed into
a 21-minute span with a peak of 38 and a mean of 20 is a spiky fan-out, and the
long poles are the single macOS job (~20 min, drawing on a separate ~5-slot
pool) and `test-units` (15-18.5 min per arch leg). Flattening those shortens the
suite and raises utilisation under any admission policy. The next section does
that for the larger of the two.

## Third change: shard the coreutils corpus

Both long poles turned out to be one package. Step-level timings from
[35638612903](https://github.com/JakeChampion/lang/actions/runs/35638612903):
`internal/coreutils` is **14m40s of the 15m08s** `go test (units)` step on
aarch64 (the job around it is 15m39s), and the same corpus is **17.4 of the
20.3 minutes** of the macOS job. Every
other package in the units lane finishes inside its shadow — `internal/ir`
4m38s, `internal/ssa` 4m17s, `internal/printer` 3m43s, and the remaining
73 packages 0.29 minutes between them. So one package was the critical path of
the entire suite, on two lanes at once.

Whether sharding helps depends on the fixed cost a shard repays. Measured by
selecting 1, 5 and 10 utilities from a single test process:

| utilities selected | wall |
| --- | ---: |
| 1 | 24.3 s |
| 5 | 62.5 s |
| 10 | 73.9 s |

Fixed cost is ~15-20 s — the native compiler, the multicall dispatcher and the
self-host compiler, each built once per process. Against 14m40s that is ~2%, so
shards are nearly free. (The per-utility increments are not linear because
`go test` already runs subtests in parallel and the utilities differ in cost;
what mattered here was only the intercept.)

A full `go test -v ./internal/coreutils/` run gives the shape, 13m45s over 253
top-level tests:

| top-level test | time | subtests | largest subtest |
| --- | ---: | ---: | ---: |
| `TestSelfHostCoreutilsParity` | 5m44s | 106 | 18.8 s |
| `TestPrintfParity` | 2m48s | 338 | 40.6 s |
| `TestMulticallParity` | 1m35s | 106 | 17.4 s |
| 250 others | ~3m36s | | |

`TestSelfHostCoreutilsParity` is one top-level test holding all 106 utilities,
so partitioning top-level names alone would pin 5m44s whole into one bucket and
make it the floor — no shard count below it would help. It is therefore sharded
by SUBTEST, which it takes well: its utilities are strikingly even (`tail` 19 s,
`sort` 17 s, `cksum` 17 s, the SHA family 16-17 s each).

`go test -run` splits its pattern on `/` before applying each part to a nesting
level, so `^(TestCatParity|TestSelfHostCoreutilsParity/cat)$` is not a valid
mixed selector — the split lands mid-alternation. A shard therefore makes two
invocations: whole top-level tests in one, its slice of the giant in the other.

Simulating the partition over the live lists, in weight units where an
unmeasured test counts 1:

| shards | per-shard | max |
| ---: | --- | ---: |
| 2 | 8.77, 8.75 | 8.77 |
| 3 | 5.85, 5.83, 5.83 | 5.85 |
| 4 | 4.38, 4.38, 4.38, 4.37 | 4.38 |
| 6 | 3.85, 2.73 x5 | 3.85 |

Four balances to within a second of even. Six is worth 12% for 50% more runner
slots and stops at 3.85 regardless, because `TestPrintfParity` is a 2m48s floor
that only splitting its own subtests would lower — not worth the selector
complexity yet. So: **four shards**.

### Measured on CI

Run [35655544770](https://github.com/JakeChampion/lang/actions/runs/35655544770),
the first with the lane in place. The test step is the two `go test` invocations;
the job also pays checkout, toolchain and the oracle cache restore.

| shard | x86_64 job / test step | aarch64 job / test step |
| --- | ---: | ---: |
| 0 | 3.7m / 3.4m | 4.0m / 3.7m |
| 1 | 4.8m / 4.4m | 4.2m / 3.9m |
| 2 | 5.1m / 4.6m | 4.7m / 4.2m |
| 3 | 4.7m / 4.3m | 4.3m / 4.0m |

The spread is 3.4-4.6 minutes of test time against a 14m40s serial package, and
the partition held on the runners as it did in simulation. What it did to the
lane it came out of:

Both metrics for both columns, because the job wall includes checkout and
toolchain while the step is the testing, and a table that mixed them would be
worth nothing as a record. Before is run
[35638612903](https://github.com/JakeChampion/lang/actions/runs/35638612903).

| | before job / `go test (units)` | after job / `go test (units)` |
| --- | ---: | ---: |
| `test-units` aarch64 | 15m39s / 15m08s | **4m54s / 4m31s** |
| `test-units` x86_64 | 12m19s / 11m38s | **7m56s / 7m30s** |

The aarch64 leg lands where the arithmetic said it would — `internal/ir` at
4m38s is now its bound, and the step is 4m31s. The x86_64 leg does not: its step
is 7m30s, still well above that, so something other than `internal/coreutils`
dominates there. The profile behind the shard weights was taken on an
aarch64-class machine, so it does not say what. That is the next thing to
measure in this lane, not a number to assume.

The lane is `test-coreutils.yml` rather than a matrix inside the units lane,
because `scripts/unit-test-packages` already drops a package that has a workflow
of its own; coreutils is the fourth. The GNU 9.12 oracle build moves with it,
since no other package in the units lane uses it.

macOS is deliberately NOT sharded here. Its corpus runs `-skip '^TestSelfHost'`,
so it excludes the 5m44s giant entirely and its 17.4 minutes are the parity side;
and macOS draws on a separate ~5-slot pool where each suite takes one job, so
three shards against two admitted suites would want six. It needs its own
analysis.

## Next measurements

Confirm the concurrent-job ceiling against the account's billing settings
rather than inferring it from queueing. Shard the macOS corpus, against that
lane's own ~5-slot pool. Separate setup/build/test time on the critical selfhost
shards and identify duplicated builds. Consider splitting `TestPrintfParity`'s
subtests if the coreutils shards ever need to go below its 2m48s floor. Profile
slow tests and generated programs before changing the compiler.

Find what dominates `test-units` on x86_64, where the leg sits at 7.9 minutes
against the 4m38s `internal/ir` bound the aarch64 leg reached.

Compare observed before/after runs rather than extrapolating. The coreutils
shard timings above are now measured on CI; the two-lane figures are still
projections from one overlap measurement, and both lanes were observed running
concurrently for the first time on 2026-09-21 without the per-round latency
being sampled. Refresh `.github/coreutils-test-weights.txt` from real runs when
the spread drifts — the rules the self-host table follows are in
`docs/CI-WEIGHT-REFRESH.md`.

## Fourth change: the self-host lane's barrier, and two workers per shard

Job-level timings for the two newest complete runs on 2026-09-22, read the way
the second measurement was: `created_at` of each job against the run's, the
runner `started_at`, and the step durations.

| Run | Event | Suite wall (min) | Ubuntu jobs | Ubuntu job-minutes |
| --- | --- | ---: | ---: | ---: |
| [35708985176](https://github.com/JakeChampion/lang/actions/runs/35708985176) | PR | 35.2 | 93 | 381 |
| [35708878302](https://github.com/JakeChampion/lang/actions/runs/35708878302) | main | 33.8 | 93 | 390 |

The critical path was the same in both, and it was not the tests:

1. `changes`, 1.2 min for ~10 s of work (queue).
2. Every independent job is created at 1.2 min. The three `warm` jobs the
   self-host shards depended on were created then too, but each waited 8-14
   minutes for a runner behind the sixty other jobs the run had just created,
   and did 45 s of work when it got one: the whole warm set is five drivers in
   45 s, ~9 s each, and fern.fern, the whole compiler, links in 16-19 s. The
   workflow's own comments still priced a driver at 50-70 s and fern.fern at
   ~236 s, which is the cost the barrier was built to avoid.
3. The twelve x86 shards, `arm64-permodule-wholecompiler` and
   `semantic-wholecompiler` are created only when the last `warm` job finishes,
   at 16.9 and 17.3 min.
4. The shards run 6.2-11.1 minutes of tests each, in one serial process on a
   four-core runner, and the last one ends at 31-33 min.
5. `verify`, a 0.2 min job, waits 1.3 min for a runner.

So a 35-minute run spent its first seventeen minutes not having created the
jobs that end it, and the shard partition was stale on top: shard 3 held three
tests of 116, 115 and 92 s, the first two weighted 15 and 13 in the file. With
the weights refreshed from these two runs the twelve shards balance to 481-541
s of observed test time each, against 373-666 s.

What changed, in the order it matters:

- **No `warm` jobs and no `needs: warm`.** Every job builds the drivers it uses
  on its own runner; the closure-keyed disk cache builds each at most once per
  job. The shards, the two whole-compiler jobs and `driver-sizes` are created
  at run start with everything else.
- **Two worker processes per x86 shard**, through `cmd/ci-test-workers` with a
  new `-weights` flag that assigns tests by the same duration-weighted LPT the
  shards are cut with. The `cli` job runs its ten isolated tests the same way:
  its two heaviest, 227 s and 212 s, ran back to back. Each process gets half
  the runner's CPUs and, through `FERN_BUILD_MEM_BUDGET_MB`, half its
  driver-build RAM budget.
- **The `cli` job no longer measures fern.fern's size and `driver-sizes` no
  longer unions four reports.** `driver-sizes` links every driver the baseline
  names, ~2 minutes, and compares the set once. It depends on nothing.
- **`test-e2e-wasm` lost its `build` jobs.** They were the same barrier one
  lane over: the test jobs were created at 6.7 and 10.8 min for ~30 s of
  compile.
- **`test-e2e-other` runs two workers per shard.** Its aarch64 leg was a single
  15-minute serial process, the longest job outside the self-host lane.
- **The seven `diff-selfhost-*` jobs are three**, one per target, each running
  the AST and the semantic leg. Four of the seven were 0.6-3 minute jobs paying
  ~40 s of setup each.
- **`TestSelfHostSemanticWholeCompilerX86_64` overlaps its phases**: the
  driver's emits run beside the gen1 self-build, and gen1's emits beside each
  other. Its job was 13.3-14.2 minutes of serial emits.
- **The x86 shards lost `continue-on-error`.** Six of six sampled runs had all
  twelve shards succeed; a reclaimed runner now reads as a failed shard, which
  is what `verify` reported for it anyway.

### Observed on the first run of this shape

Run [35724456242](https://github.com/JakeChampion/lang/actions/runs/35724456242),
the pull request's own run on its final head, under the usual contention (a
main run and the other PR lane both live).

| | before (35708985176) | after |
| --- | ---: | ---: |
| Ubuntu runner jobs | 65 | 62 |
| Ubuntu job-minutes | 381 | 340 |
| self-host shards created at | 16.9 min | 1.3 min |
| self-host shard wall | 7.0-11.8 min | 6.7-10.4 min |
| `test-e2e-other` aarch64 / x86 shards | 15.1 / 8.9, 12.0 min | 9.9 / 8.9, 6.2 min |
| `semantic-wholecompiler` | 14.2 min | 12.2 min |
| `cli-driver-tests` | 10.2 min | 6.2 min |
| suite execution (first job start to last job end) | 34.0 min | 30.3 min |
| queue wait before the suite | 0.5 min | 28.4 min |

The barrier is gone and the job-minutes fell 11%, and the suite still ran 30
minutes, because the shards spent 13-20 minutes queued for a runner after
being created: with three suites live, ~180 jobs share the 40 slots, and a
job created at minute 1 gets its runner when one frees. The self-host shard
walls moved only ~10% because two workers take a cold shard from 625 s to
~400 s on a four-core box (measured below) and the CI shards are each ~6
minutes of that already. What the run does show is the shape: every job
is created at run start, so nothing in the lane waits on anything but the
pool. The 28-minute wait before the suite is the PR FIFO, which the second
measurement covers.

The same run's macOS job was 14.9 minutes, 769 s of it the coreutils corpus
step. The per-case log of main run 35708878302 (the corpus took 911 s there)
puts 300 s of that on one case: `seq -f "%.2147483648g" 1`, whose GNU 9.12
oracle never returns on macOS and ran to the harness's five-minute limit on
every run before #10003. The case now carries a ten-second bound of its own
(the outcome, "did not finish", is unchanged, so the Darwin ledger does not
move). Measured on #10003's own run 35733713188: `TestSeqParity` 307 s to
18 s, the corpus step 701 s, and the job 896 s again, the same as before,
because the rest of the corpus took 678 s where run 35724456242 had it at
462 s and run 35708878302 at 604 s, on the same code. The macOS runner's
speed varies by that much between runs (`go vet` 22 s and 31 s, the
self-host native step 55 s and 82 s, on the two runs above), so a change of
a minute or two in that job is below the noise, and the 200 s
`TestMulticallParity` is the largest item left in the corpus.

### The concurrent-job ceiling, confirmed

The account is GitHub Pro. GitHub's published limits for standard hosted
runners are 40 concurrent jobs for Pro (20 Free, 60 Team, 500 Enterprise),
with at most 5 macOS jobs at once on every plan below Enterprise, and the
macOS 5 is shared with larger runners. Public repositories are not treated
differently. GitHub Support can raise the number on request. The 36-37
observed in the second measurement is that 40 less lint, the reapers and
`check-sources`.

That ceiling is why the lane changes above remove jobs and barriers rather
than adding shards: with two pull requests and a main push in flight, ~180
jobs compete for 40 slots, so a job created late is a job that waits, and a
job that idles its runner is a slot the next suite does not get.

### What was found and not fixed here

The `diff-selfhost-wasm` jobs have been green while running nothing: the two
wasm differential legs skip without wasmtime and the job never installed it.
Run with it, five seeds trap in the self-host wasm module where the
interpreter runs clean (#9995). That is an emitter fix, not a workflow one; the
job keeps its shape and its comment says why.

### Next measurements

Compare the first runs on this shape against the table above: suite wall,
shard wall, and whether two workers hold a shard's driver builds inside the
runner's RAM. The macOS job (14-18 min, 12-15 of it the coreutils corpus in
one process on a 3-core runner) is the next long pole once the Linux side is
under it; its own ~5-slot pool is what bounds sharding it. `changes` costs 1.2
minutes at the front of every run for a listing call.

## Fifth change: a superseded pull request run is cancelled server-side

### What the pool was doing during one PR run

Run 35733713188 (#10003's final run, 13:28-14:01 UTC) never reached the
ceiling on its own: its jobs peaked at 25 running, on a pool of 40. The rest
of the pool, per the run listing for the same half hour:

| Holding slots alongside it | Slots and duration |
| --- | --- |
| `CI main` for 95c7867 | ~60 jobs, 13:04-13:46 (42 min, itself starved) |
| the other lane's PR suite (#9990) | ~60 jobs, 13:32-13:56 |
| two runs a push had superseded (35733119494, 35733509031) | their jobs kept running until 13:36, 8 and 4 minutes after the push that made them obsolete |
| Pullfrog review runs | four dispatches of 15-35 min, one slot each |

The superseded runs are the part this repository controls. They were
cancelled by `reap-stale-runs.yml`, which needs a runner of its own to act and
queues for it behind the very jobs it is meant to free. Over its last 31
successful `pull_request` runs the sweep took a median 0.9 min from trigger to
completion, p90 7.5, maximum 14.7. The `changes` and `Lint` jobs of the
measured run, ten seconds of work each, waited 6 minutes for a slot at
13:28-13:34, which is exactly the window in which the two superseded runs
were still executing.

### The change

`ci.yml` carries a concurrency group per pull request with
`cancel-in-progress` on for `pull_request` events only. A push then cancels
the previous run the moment it lands, server-side, and nothing waits for a
runner to do it. The suite FIFO is untouched: it stays on `ci-suite.yml`,
where `queue: max` forbids `cancel-in-progress`, so the two groups nest (one
run per PR on the outside, two suites at a time on the inside). A main
validation and a dispatch get a run-unique group and are never cancelled.
`reap-stale-runs.yml` no longer triggers on `synchronize`; it remains the
backstop for closed pull requests and the quiet-hours cron.

Measured on the first push after it merged (#10021, 17:01:17 UTC): the
superseded run was cancelled at 17:01:24, seven seconds later, with no runner
involved. The first main validation after the merge (run 35755577198) ran
its suite normally; the fallback group's prefix differs from ci-suite.yml's
on purpose, because an identical one has the suite request the group its own
run holds and GitHub cancels that as a deadlock, which a PR run cannot show.

### Whether the two-lane queue is still needed

Yes. A suite on an otherwise empty pool (run 35745901102) is 323 job-minutes
over an 18.6-minute wall, a mean of 17 slots in use out of 40: it holds 35-36
running for its first four minutes, is under 25 by minute seven with nothing
waiting, and spends its last five minutes on a handful of jobs. A second
suite fills that decay; a third would put about 180 jobs against 40 slots at
fan-out and stretch every suite without adding throughput. Removing the queue
would be worse still: of the 100 completed pull-request runs before 16:18 UTC
on 2026-09-22, 72 were cancelled as superseded and 28 succeeded. A run
waiting in the queue costs nothing when its push is superseded, where a run
that had fanned out would have spent slots on work that is discarded.

## Sixth change: the fernsmith shrink sweep runs its seeds in parallel

`TestGenBytesShrinkIsMonotonicAndValid` under `RUN_SHRINK_PROPERTY=1` is
nearly the whole fernsmith lane. It was parallel across its three entry
points only, so it ran three-wide on a four-core runner. Each (entry point,
seed) pair is now a parallel subtest (#10021).

| | before | after |
| --- | ---: | ---: |
| local four-core box, the sweep test alone | 472 s | 386 s |
| `test-fernsmith-x86_64` test step (runs 35733713188, 35757937523) | 587 s | 559 s |
| `test-fernsmith-aarch64` test step | 375 s | 329 s |

The runner gained a fraction of what the local box did: the x86_64 runner's
fourth vCPU is worth much less than a core to this CPU-bound sweep. The
lane's floor is now the per-seed type-check cost itself.

### Measured and left alone

- Per-job setup (checkout, toolchain, `go test -c`) is 29 of the 328
  job-minutes of a suite, a mean of 28 s per job. Merging small jobs would
  not repay the longer critical path it creates.
- `test-units-x86_64` is bounded by three serial packages, `internal/ir`
  (392 s), `internal/ssa` (351 s) and `internal/printer` (349 s), which
  run concurrently with each other. None can take `t.Parallel`: `ir`'s
  tests alone write the `internal/ast` package globals 143 times.
- The failure reaper (`cancel-on-failure.yml`) waits for a runner like the
  stale-run reaper did: on run 35749155333 the failing shard finished at
  16:13:07, its lane concluded at 16:15:58, and the reaper was still queued
  minutes later. Test jobs keep a read-only token by a pinned decision
  (`TestCILintRunsOutsideTheFullSuiteQueue`), so an in-job cancel step is
  not available; the cost is bounded by the lane's own tail.


### Not done: skipping a main run whose tree a PR run already passed

A rebase merge of a branch that is level with main produces the same tree
the PR run tested, so the main validation of that push would re-run the same
tests on the same tree. Measured on the last 40 merged pull requests: 9 had
the merge commit's tree equal to the PR head's tree, 31 did not, because main
moved between the PR's last push and its merge (branches here are rarely
brought level before merging, and `auto-rebase-prs.yml` rebases them
afterwards). A tree-hash skip would remove about one main run in five; the
per-run `changes` filter and `ci-main.yml`'s coalescing already remove more,
and the skip would need the PR run to have run every lane. Not worth its
own machinery at that rate.

The seventh change builds it anyway, per lane rather than all or nothing,
which removes that last objection: a lane the PR run skipped still runs on
main.

## Seventh change: split the long poles, and skip what a change cannot reach

### Splitting the longest jobs

Measured on #10047's green run (35819228993) against the two full runs before
it (35745901102, 35757937523):

| Job | before | after |
| --- | ---: | ---: |
| macOS | 15.8 min, one job | 7.6 and 12.0 min, two shards |
| differential aarch64 | 10.8 min, one shard | 5.8 and 5.8 min |
| differential x86_64 | 9.7 min, one shard | 5.2 and 5.1 min |
| fernsmith x86_64 | 9.7 min | 5.5 and 4.6 min |

The macOS corpus halves took 429 s and 432 s; shard 1 also carried every
other step, 250 s of them, which the next commit rebalances by moving the
162-second self-host native step to shard 0. On the first two main runs
after the merge (35828177487, 35828545177) the two shards took 6.6 and 7.5
minutes, then 8.0 and 8.2. The ratchet moved to a Linux job
that reads both shard logs, and passed on its first run.

The semantic whole-compiler job (11-12 minutes) was not split: it is a chain in
which gen1 must exist before gen1 can emit, so two jobs would add a hand-off to
the same chain.

### A premise that did not hold: job order

The same PR first listed lanes longest first, on the reading that the
12-minute semantic job started six minutes late on an idle pool (35745901102)
because it was listed seventh. That reading was wrong. On that run, lanes
listed after the self-host lane started at once; only the self-host lane's own
26 jobs waited, whatever their position. On 35819228993, with the order changed,
the self-host lane's jobs again started across 22 minutes. The reorder was
dropped before merging.

### Lanes a self-host-only change cannot reach

26 of the 74 commits on main from 2026-09-15 to 2026-09-22 touched only
`examples/self_host` or `internal/e2eselfhost`, and every lane ran on each. The
x86_64 e2e, differential, fernsmith, fuzz and examples lanes select no test
that reads either tree, and nothing outside the self-host lane imports
`internal/e2eselfhost`. Run locally with both trees deleted, all five passed:
fernsmith, a 1/32 slice of the differential sweep, the fixture corpus, the
whole `^TestX86_64` set (813 s) and every example build. Those lanes now ignore
both trees, and each of their jobs deletes them right after checkout so a test
that starts to read them fails on every run. The wasm, arm64 and e2e-other
lanes keep running: each selects tests that build or read the self-host
sources.

### Main runs that re-test the PR's tree

Main ran every lane on every push. A rebase merge of a branch level with main
pushes exactly the tree the PR's run tested; 9 of the 40 merges before
2026-09-22 did. On a main push, the lane selector now finds the merged PR, and
when its head's tree equals the pushed tree it skips each lane that passed on
the PR's successful run at that head. A lane the PR did not run
still runs on main, and perf always does, since its main run records the perf
history.

On the first two main pushes after it merged, neither qualified: #10049's and
#10059's heads were not level with main when they merged, so their trees differ
from the pushed trees and every lane ran, as it should. The selector's log said
only "a push runs every lane", which does not distinguish that from a failure
to find the PR. It now names which of the three cases applied: no merged PR, a
different tree, or no successful run at the PR's head.

### Review runs

Pullfrog review runs, one standard runner each for 15-35 minutes, are capped
at two at once by two concurrency groups with `queue: max`. The ninth
measurement below found most of them starting after their pull request had
merged; the gate step in pullfrog.yml and the two reapers now end those.

### Not done: runners outside the 40-job limit

The Team plan is out. The other way past the 40-job ceiling is a runner
provider whose runners GitHub does not count against it; that needs an account
with the provider and its GitHub App installed on the repository, which only
the owner can do. Once it exists, moving a lane is a `runs-on` label change.

## Eighth measurement: where a day of runner time goes

Every job that ran on a runner in the 24 hours to 2026-09-22 18:00 UTC, summed
by what started it. Jobs cancelled before a runner took them are not counted.

| Source | Job-minutes | Share |
|---|---:|---:|
| PR runs that finished | 19,000 | 40% |
| PR runs cancelled by a later push | 13,400 | 28% |
| Main runs (47) | 12,900 | 27% |
| Pullfrog reviews (118) | 2,000 | 4% |
| Total | 47,800 | |

47,800 job-minutes is 83% of what 40 slots provide in a day, so the pool is
busy enough that the queue, not the runners, sets how long a PR waits.

Of the cancelled PR time, 9,900 job-minutes across 81 runs were superseded by
a push that changed code and 3,000 across 24 runs by a push that only merged
main. 34 of the day's 170 PR runs were started by a push that only brought
main in.

### Pushing while a run is in flight

A push cancels the PR's run in flight, and everything that run had done is
thrown away. CLAUDE.md now asks agents to hold further fixes, review nits
included, until the run reports, unless it is already red.

### The up-to-date requirement stays

The main ruleset requires a PR to be level with main before it merges. That is
what the 3,000 main-merge job-minutes above pay for, and the owner has chosen to
keep it. It also means a merge that honours the requirement pushes the PR's own
tree, which is the case the identical-tree skip above serves.

### Main lanes whose inputs did not change since they last passed

A main push that is not a level merge still ran every lane. The selector now
also looks, per lane, at the newest completed `ci-main.yml` run that ran that
lane. If the lane passed there, and the compare from that run's
commit to the pushed commit is a fast-forward listing fewer than 300 files, a
lane whose filter selects none of those files is skipped. A lane that failed on
that run runs again, as does perf. A lane a later push cancelled proves
nothing, so the search continues to an older run; main runs are coalesced, so
the newest completed run is often a cancelled one. A failed API call, a base
that is not an ancestor, or a compare that may be truncated runs every lane
with a warning in the job summary.

Both rules judge a lane by its jobs that ran: it has passed when at least one
job succeeded and none failed, timed out or was cancelled. Jobs the lane's own
conditions skip do not count against it. The first main run after the fix to
#10105's bootstrap tests (447, 41bc326) was a level merge and skipped 15 lanes
on #10116's run, but Bootstrap ran anyway: a called bootstrap.yml always skips
`publish` and `release`, and both rules then required every job to succeed, so
Bootstrap never counted as passed. The last-pass search also walked all 30 runs
looking for one, and the selector step took two minutes.

## Multi-core test execution: what parallelism can and cannot buy

Measured 2026-09-22 on the 4-core container (Xeon 2.10 GHz, a 14 GB cgroup),
always the same x86 shard 5 of 12 (218 self-host tests plus 5 residuals)
from the same test binaries, so every row is comparable. "Cold" is a fresh
driver disk cache, which is what every CI shard starts with.

| shape | wall | CPU (user+sys) |
| --- | ---: | ---: |
| one serial process, cold | 625 s | 880 s |
| two worker processes (`ci-test-workers`), cold | 397 s | |
| one process, `t.Parallel` in every test, `-parallel 4`, cold | 392 s | |
| four worker processes, cold | 372 s | |
| one serial process, warm | 389 s | 447 s |
| one process, `t.Parallel`, `-parallel 4`, warm | 215 s | 397 s |

Three facts follow.

**The e2e packages are safe to run in parallel.** A blanket `t.Parallel()`
in every top-level test of `internal/e2eselfhost` (1,448 files) and
`internal/e2e` (842 files), injected by script and excluding the thirteen
files that set an env var, change directory or assign a package global,
ran this shard twice with no failure. The harness's caches were already built
for it: per-key `sync.Once` builds and the RAM reservation.

**A serial shard uses 1.15 cores, and parallelism recovers most of the rest —
once the drivers exist.** Warm, four parallel tests are 1.8x faster than
serial. Cold, every shape lands at 370-400 s, because the shard builds 21
distinct drivers (~9-20 s of multi-core emit each, ~240 s of the cold
serial run) and those emits are serialised by the RAM reservation and bound
by the same four cores. The 811 small program links in the same cache are
8 ms each and do not register. So the two-worker shape the fourth change
shipped is worth ~1.57x on a cold shard, and switching it to in-process
`t.Parallel` would tie it; the next gain on the self-host shards is a
cheaper driver emit (docs/LOCAL-DEV-LOOP.md: the IR passes are 54% of a
whole-compiler emit and GC 28%), not more parallelism.

**`internal/e2e` cannot use `t.Parallel` today, and the reason is a compiler
design choice, not the tests.** The same blanket injection on the x86_64
lane fails 30 top-level tests, every one of them a differential that flips
a compiler-wide switch and compiles both ways: `ast.RcFreeEnabled` (402
assignment sites in tests, 140 readers in the compiler), `OwnedByDefault`,
`EnumRcPayloads`, `BorrowInferEnabled`, `RcReuseEnabled`, `LeakCheckEnabled`,
`TwoWordOverride` and seven more, fourteen package-level variables in
`internal/ast` read from `checker`, `monomorph`, `ir` and both backends.
Two tests flipping the same global at once corrupt each other's
comparison. Under `t.Parallel` that lane did use 2.9 cores (5m23s wall for
15m45s of CPU), so the throughput is there; the correctness is not. Making
those switches per-compilation options carried through `checker.Check`,
`monomorph.Run` and the emitters, instead of process globals, is the work
that lets the native-backend lanes run in one parallel process, and it is
also what a `fern` CLI flag for each of them wants. Until then those lanes
stay on worker processes, where isolation comes from the process boundary.

## Ninth measurement: October 1, 2026 — where the rounds went, and what main re-ran

Every `ci.yml` pull-request run created from 2026-09-28 00:00 UTC to
2026-10-01 19:00 UTC, every `ci-main.yml` run in the same window, and the job
and step timings of three green pull-request runs, read the way the earlier
measurements were. The cancelled-run figures are a sample of 25 of the 470.

| | count | share |
| --- | ---: | ---: |
| PR runs created | 600 | |
| cancelled (a later push, a merge, or a reaper) | 470 | 78% |
| succeeded | 121 | 20% |
| failed | 5 | 1% |
| main runs created | 191 | 2.1 per hour |
| main runs coalesced away (cancelled pending) | 79 | |
| main runs succeeded / failed | 84 / 26 | |

A green pull-request round, creation to conclusion, was 64 minutes at the
median, 114 at p90 and 178 at worst, against 33 / 50 / 73 on 2026-09-21. The
suite itself grew: 419-495 Ubuntu job-minutes per run against ~330 then, and
the self-host x86 shards' test step went from 9.4 minutes (run 35724456242,
2026-09-22) to 18.4 (run 36815704665, 2026-10-01) with the same workflow
steps around it. The sum of the measured weights in
`.github/selfhost-test-weights.txt` went from 6,474 s to 10,655 s over the
same nine days while the package gained 5% more files, so the growth is new
tests of 100-180 s each, not more tests. On a saturated pool each of those
shards then waited 20-25 minutes for a runner before its 15-19 minutes of
work, which is why the round doubled when the shard did not.

Where the pool's minutes went, per hour, over the window:

| Source | Job-minutes per hour | Share of 2,400 available |
| --- | ---: | ---: |
| PR runs later cancelled (5.2/h at a mean of 163) | ~850 | 35% |
| PR runs that finished (1.4/h at ~470) | ~630 | 26% |
| main runs that finished (1.2/h at ~510) | ~610 | 25% |
| Pullfrog reviews (3.3/h at a mean of 21.5) | ~72 | 3% |

Two of those rows were the same work done twice.

### Pull requests merge while their run is in flight

Of the twelve most recent successful main runs, eight ran every lane (71
jobs, 510-533 job-minutes) and four ran only perf. The eight were the common
case, and the reason was not the tree: for the five checked, the merge
commit's tree equalled the pull request's head tree in three, which is what
the ruleset's up-to-date requirement produces. The reason was that the pull
request's run at that head was still running when the pull request merged,
so cancel-on-merge.yml cancelled it, and the identical-tree skip, which asked
for a *successful* run, found none.

| Pull request | Suite checks green at the merge | Still running or cancelled |
| --- | ---: | ---: |
| #10934 | 57 | 13 |
| #10925 | 0 | 66 |
| #10916 | 26 | 44 |
| #10868 | 1 | 68 |

The ruleset requires only `Lint / lint` and the arm64 e2e job, and the
repository owner merges with the rest in flight. So a pull request's run and
the main run after it were testing the same tree at the same time, one of
them to be thrown away, and the main run had to go the full 40 minutes before
a self-host regression showed: 25 of the 26 failed main runs failed in the
self-host lane, the one that ends last.

The main validation now **adopts** that run instead. On a push to main whose
tree equals the merged pull request's head tree, ci.yml's lane selector reads
the newest pull-request run at that head whatever it concluded, lane by lane:
a lane whose every job succeeded is skipped, one that failed, was cancelled
or did not run runs on main. A run still in flight with its suite under way
is waited for, polling every 30 s for up to 30 minutes
(`CI_CHANGES_WAIT_MINUTES`); a run whose suite is still queued behind the PR
FIFO is not, since main alone is faster than the queue. cancel-on-merge.yml
spares exactly that run on the close event, and reap-stale-runs.yml for 45
minutes after the merge; both still reap the rest of the branch's runs. The
selector and the reapers each read "suite under way" at their own moment, so
a suite that starts between the two reads is tested twice as before, once
more; that costs a suite, never a result.
Expected effect: one suite per merge, not two, and main's verdict on the
self-host lane at the moment the pull request's run reaches it rather than
40 minutes after the merge.

The run that pull request's branch gets after the merge is unchanged in
cost: the pushes keep coming at the same rate. Of the 470 cancelled runs,
only 9% were superseded within five minutes of starting and 17% within ten
(median gap 35 minutes), so a settle delay before the suite fans out was
measured and not built: it would save under a tenth of that row for five
minutes added to every round.

### Reviews that started after the merge

Of 60 consecutive successful Pullfrog runs, 43 began after their pull request
had merged or closed, and at 19:00 UTC on 2026-10-01 the review queue held 21
unfinished runs, 17 of them for closed pull requests, two of them since
01:40 UTC. The queue is two slots and reviews arrive at 3.3 an hour at 21.5
minutes each, so it runs about 60% utilised but in bursts, and once behind it
serves reviews nobody reads before the ones somebody is waiting on.

Three changes. pullfrog.yml gains a gate step before the agent: a run whose
pull request is closed, or that a newer run of the same kind for the same
pull request has already superseded in the queue, ends in seconds. The
superseding rule is by kind on purpose: a whole-PR review is never dropped
for an incremental one. cancel-on-merge.yml cancels the closed pull request's
queued reviews on the close event, and reap-stale-runs.yml sweeps the review
queue for closed pull requests on its cron, so a review the gate never
reaches (one of the two slots is held by a 50-minute review) still goes.

What stays with the Pullfrog dashboard, outside this repository: the
"review new commits" trigger fires on every push, and 166 of the 300 review
runs in the window were that kind, on pull requests that were pushed to five
to ten times. The gate collapses the queued ones; the trigger decides how
many are dispatched.

## Tenth change: the stage0-built drivers are carried between runs

Since 2026-09-29 the self-host test drivers are compiled by the pinned
stage0 compiler instead of the Go backend (docs/LOCAL-DEV-LOOP.md): 139 s
for `asm_run.fern` on the 4-core container, measured for this change, where
the Go backend took ~9 s. That is the whole of the shard doubling in the
ninth measurement. Every job built its drivers from nothing: the 12 x86
shards each build the stock drivers their slice touches (`asm_ir_run.fern`
has 671 build sites in the package, `wasm_ir_run.fern` 323, `asm_run.fern`
263, `fern.fern` 148), the three `diff-selfhost` and three
`fixtures-selfhost` jobs run `go test` with no disk cache at all, and the
`test-e2e-other`, `test-e2e-arm64`, `test-e2e-wasm` and macOS jobs run
`internal/e2e` tests with 46 driver build sites, also with none.

The harness already keys a driver on its source closure, the target, the
stage0 binary's bytes and the stdlib tree (`CachedDriverBinFor`), so the
same key is the same bytes on any runner. `.github/actions/selfhost-driver-cache`
restores the harness's disk cache directory from the Actions cache under a
key that hashes the same inputs (`bootstrap/stage0.lock`,
`examples/self_host/**`, `internal/stdlib/**`), with a prefix fallback to
the newest earlier directory, in which every driver whose own closure did
not change still matches. Only `*.driverbin` files travel; entries older
than seven days are dropped after the restore. Every job that builds
drivers restores; one job per lane saves, the one that builds every stock
driver anyway (`driver-sizes` on Linux x86, the macOS shard that runs the
self-host arm64-darwin tests), so the shards do not each upload a
near-identical directory per tree. Measured locally against a populated
directory, the same build is 0.64 s.

The cache is one directory per runner OS and arch, not per target, and that
is enough: a driver is the self-host compiler built to run on the job's
host, whatever target it then emits for, so the arm64 and wasm legs on an
x86 runner (`diff-selfhost-arm64` builds `asm_modload_run.fern` as an x86
binary) want the same x86-64-linux stock drivers `driver-sizes` saves.
Across both e2e packages only 11 build sites ask for another target (9
arm64-linux, 1 wasm32-wasi, 1 arm64-darwin), each a per-test program rather
than a stock driver; those build cold on every run, as before. On a Linux
ARM64 runner the shards skip the x86 drivers and build arm64-linux ones
natively, and the self-host lane's aarch64 shard saves that directory. An
x86 directory is ~105 MB uncompressed (the rows of
`.github/selfhost-driver-sizes.txt`), stored zstd-compressed by
actions/cache; with three savers, up to three directories per tree.

Measured on the change's own first run (36917972646), before any cache had
been saved, so from the shared disk directory alone: `diff-selfhost-x86_64`
17.3 to 5.4 minutes and `diff-selfhost-arm64` 14.5 to 5.7, the three
`go test` invocations of each job no longer each rebuilding the drivers.
Splitting those jobs into seed shards was prepared and dropped on that
number: two more runner slots for about two minutes.

What it cannot do: the first run on a tree whose self-host sources or
stdlib changed builds cold on every shard as before, and saves; the next
run at that tree (a merge-main push, the main validation when it is not
adopted) hits. A pull request's run restores caches saved on its own
branch and on main; main's cache is refreshed only by a main run that
runs the lane. Branch-scoped caches and the 10 GB repository limit are
why the shards do not save.

## Eleventh change: the arm64 gate is three shards

The main ruleset requires two checks, `Lint / lint` and
`Full suite / Test e2e arm64 / test-e2e-arm64`, and pull requests here merge
when those two are green. Lint is 3.5-4.7 minutes. The arm64 job was one
job running ~690 `TestArm64*` tests in two worker processes: 8.0-9.1 minutes
of wall on the three green runs of the ninth measurement, after 7-10 minutes
queued for an arm64 runner. So the merge gate was that one job.

It is now three shards of equal count (`scripts/shard-tests` with no weights
file), each in two workers, behind a leaf job that keeps the required check's
exact name and fails unless every shard succeeded, a vanished runner
included. Three arm64 runner slots instead of one, for a gate of roughly a
third of the wall plus the leaf's queue wait. The `test-e2e-other` aarch64
leg, one shard of 9.9-12.2 minutes and the longest job outside the self-host
lane, is two shards by the same arithmetic.

`selfhost-fixpoints-x86_64` ran four whole-compiler proofs in one job, 2.0,
2.4, 2.6 and 3.6 minutes of them after a shared fern.fern build: two jobs of
two proofs each now, the build coming from the driver cache in both.

`test-e2e-wasm` ran its 9.0-9.8 minutes in one serial process per host
while every test waited on a wasmtime subprocess; it runs in two isolated
worker processes now, as the other e2e lanes do. The self-host lane's single
aarch64 shard (8.5 minutes) is two.

Lint, the other required check, spent 2.0 of its 4.7 minutes in
`fern -check sources`, and 146 of those seconds locally were
`tools/selfhost_driver_check.sh` type-checking 47 drivers one after another,
each reading the compiler's closure again. The loop runs one check per core
now (`xargs -P`): 44 s on the 4-core container, and the stdlib loop beside it
16 s to 5 s. The `fern test cache` step (1.2 minutes) stays serial: its two
probes edit sources and read `go test`'s cache verdicts, so running them at
once would have each probe invalidating the other's cached result.

Sharding it found a bug in `scripts/shard-tests`: with an empty weights file
(the `/dev/null` fallback a lane without weights gets, or a file of comments)
awk's `NR == FNR` idiom read every test name as a weight row, and every shard
printed nothing. The self-host lane's shard step then exited 0 on an empty
selection, which would have been thirteen green shards running no tests.
The weights are now read in `BEGIN`, the partition is unchanged when the
weights file has rows (checked on the live self-host list), the empty
selection exits 1 in every lane, and `TestShardTestsCoversTheListWithAndWithoutWeights`
runs the script with an empty, a comment-only, a missing and a real weights
file and checks the buckets partition the input.

## Twelfth change: the remaining single long jobs, and the gate's other half

With the self-host shards shortened, the next long poles were single jobs:
`test-e2e-x86_64` at 13.3 minutes (two shards now, like the arm64 gate),
`test-units-x86_64` at 10.4 (two jobs per host: `internal/ir`, `ssa` and
`printer`, each 350-390 s of serial tests that cannot t.Parallel, apart from
the other 75 packages they were sharing four cores with), and the self-host
lane's shard 0, whose `TestSelfHostAssumeEligibleByteIdenticalX86_64` is
503 s of CPU-bound work already running four wide (83 checked per-process
emits, each paying the whole-program parse floor). No weight can place a
test that size without making its shard the longest, so it runs in a job of
its own and leaves the shard partition. The test, and its job, went with
`-assume-eligible` when the typed lowering lost its off switch (#10980).

Lint, the gate's other half, ran twelve steps one after another. The eight
cheap, read-only ones (`vet`, `gofmt-check`, `fmt-check`, `deadcode`,
`actionlint`, `testnames`, `ci-selftest`, `digest-check`) are one
`make -j -k lint-fast` step now, 85 s on the 4-core container for all of
them; `check-sources` and `fern-test-cache` keep their steps, the first
because it is the long one and the second because its probe edits a source
file while it runs.

### A failed test is readable without its log

Every test job now ends its test steps with `scripts/ci-annotate-failures`
on the test2json streams gotestsum writes (`GOTESTSUM_JSONFILE`, set once
per workflow) and the `worker-*.jsonl` files `cmd/ci-test-workers` leaves
behind. It runs only after a failure and prints one `::error` annotation
per failed test, with the last lines of that test's output, up to the ten
GitHub keeps per step. The annotations are what the Checks tab shows and
what the REST API serves without a redirect to blob storage, so a failure
is readable from the places a job log is not: a cancelled run after the
failure reaper, a network that refuses the log redirect, a phone. Before
this, the coreutils lane's red on main could be seen but not read from
here, and root-causing it meant reproducing locally against a different
coreutils oracle. `TestTestRunningJobsAnnotateTheirFailures` pins one step
per test-running job, after its last test step.

### Next measurements

Read the `selfhost-driver-cache` step's line on the first shards after
this merges: "N driver(s) restored" against the job's test-step time. A
shard that restores its stock drivers and still runs 15 minutes has its
time in tests, and the next target is sharing the per-test driver variants
or the tests themselves.

Confirm on the first day's main runs how many
adopted a pull request's run,
and how long the selector waited: the `changes` job's log and summary say
both. The self-host lane's growth is the next cost to attack and it is in
the tests, not the workflow: each 100-180 s test builds a driver of its own
through the closure-keyed cache, and sharing drivers across tests with the
same closure is what would bring the shard back under ten minutes. The
suite's job-minutes, not the queue policy, now set how long a round takes.
