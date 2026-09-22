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
