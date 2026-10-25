# Pull request CI: what the queue was waiting on

Status: [record] measurements of 2026-10-25 and the changes they drove.

## Where a pull request's time went

Run [40157497455](https://github.com/JakeChampion/lang/actions/runs/40157497455)
is a green pull request run from 2026-10-25, read with the jobs API.

| Measure | Value |
| --- | ---: |
| Wait for a suite slot (`pr-ci-*` FIFO) | 128 min |
| Suite wall time after its slot opened | 80 min |
| Whole run, push to green | 221 min |
| Jobs in the run | 121 |
| Runner minutes in the run | 958 |

Inside that 80 minutes, the suite's own jobs each waited 30 to 55 minutes for
a runner. Two suites, main and the other workflows share one ceiling of about
37 concurrent jobs. Per-job setup was not the cost: the
shared actions restore every cache in about 10 seconds.

Runner minutes by lane, from the same run:

| Lane | Runner minutes | Jobs |
| --- | ---: | ---: |
| Test e2e self-host | 337 | 35 |
| Test e2e other | 104 | 6 |
| Test units | 60 | 10 |
| macOS arm64 | 42 | 3 |
| Test coreutils | 39 | 8 |
| Test fernsmith | 35 | 3 |
| Every other lane together | 100 | 56 |

## What the two heavy lanes were doing

**The modload test driver compiled every large program in sequential child
processes.** For a program above 1500 functions,
`compiler/drivers/asm_modload_run.fern` forked itself in batches. Each child
parsed and loaded the whole program again. The limit came from the arena era,
when one process was too small for the compiler to compile itself.

`strace` on
`TestSelfHostServeRecvDeadline` showed the driver run three times for one
program: 13 seconds of front end, then two children of 12 seconds each.
`bin/fern` compiled the same program in one process in 7 seconds at 385 MB
peak RSS. The tests that build
programs through that driver (the serve, HTTP handler, DNS and fetch tests,
212 tests in 77 files) carried 36% of the lane's measured weight.

The driver now emits in one process, as the CLI does. The explicit
`-per-module-emit-all` and `-spawned` modes stay for the tests that cover
them. `drivers/asm_load_run.fern` lost the same 512 to 1500 function
per-module path.

| Test | Before | After |
| --- | ---: | ---: |
| `TestSelfHostServeRecvDeadline`, warm driver, 4-core container | 32.5 s | 10.2 s |

**The other lane ran stdlib suites under the interpreter a second time.**
218 `TestRunner*ExamplePasses` tests each ran a `tests/stdlib/*_test.fern`
suite through the Go interpreter. Every one of those suites is also a case in
`selfHostStdTestCases`. The self-host stdtest lane runs each case under the
interpreter and compiled, and compares the two. The interpreter is slow on the
crypto suites: `tls_server_test.fern` was weighted 1169 s there. The compiled
program builds in 6.3 s and runs in 0.13 s.

The 218 tests are deleted. The stdtest cases now also require the interpreter
run to exit 0, the check those tests carried. The 42 `TestRunner*` tests for
suites outside the stdtest cases stay.

## The queue

A pull request learned it was red only after its suite reached the front of
the queue. The fixture corpus legs (`fixtures-selfhost-*`) moved out of the
self-host lane into `.github/workflows/preflight.yml`, which ci.yml runs beside
Lint, outside the suite FIFO. A red leg cancels the run through
`reap-preflight`, queued suite included, so the slot goes to the next pull
request.

The self-host lane's x86-64 shard count went from 12 to 9: the lane lost the
fixture legs and about a quarter of its weight, so nine shards carry the load
twelve did. The weights file is unchanged. Its rows for the modload tests now
read high, which costs balance only, and the next refresh
(`docs/CI-WEIGHT-REFRESH.md`) corrects them.

## The fast tier

Even at 600 runner-minutes a suite is 15 minutes of the whole pool. Pushes
arrive about every 12 minutes, so the queue returns at every busy hour. The
second change moves the full suite off the pull request: a push runs Lint and
Preflight, where Preflight also calls the x86-64 legs of the unit, e2e, wasm,
differential and coreutils lanes (`hosts: x86_64`). The full suite runs on main
after the merge and on a pull request labelled `ci-full`.

Preflight runs only where the suite does not. The suite holds every Preflight
leg, so a run that takes both repeats 16 jobs. Main's first full runs did so
while the pool queued jobs for 22 minutes.

The owner chose this on 2026-10-25. A regression only the heavy or aarch64 lanes catch now lands on
main and is caught by main's run.

## Drivers built once

Every x86 job of the self-host lane built the stage0 drivers its tests need.
The jobs started together, so on a compiler change all of them missed the
driver cache. `driver-sizes` already built every stock driver and saved the
cache. The other x86 jobs now `needs:` it, so they restore that cache and build
only a test-specific driver. The lane runs on main and on `ci-full` pull
requests. There, the five minutes of latency the edge adds cost less than a
driver build in each of some thirty jobs.

## Fewer jobs

Run 40179239803, main's full suite before the drivers-once edge, ran 97 jobs
for 705 runner-minutes. 41 of those jobs ran under four minutes, and a job's
setup (checkout, toolchain, test binaries) is one to two minutes of that.
Jobs waited 10 to 35 minutes for a runner. On a pool that queues, a job slot
costs more than the minutes it saves, so the small jobs fold:

| Lane | Before | After | Why |
| --- | ---: | ---: | --- |
| Self-host whole-compiler proofs | 5 jobs | 0 | The seven tests measure 48 to 165 s each since #13197. They ride the shards with weight rows. |
| Test units, heavy group | 4 shards per host | 2 | About 4 minutes of tests per shard after setup. |
| Test coreutils | 4 shards per host | 2 | About 3.5 minutes of tests per shard after setup. |
| Test e2e differential | 2 shards per arch | 1 | About 3 minutes of sweep per shard after setup. |
| Test e2e x86_64 | 2 shards | 1 | About 90 seconds of tests per shard. |
| Test e2e arm64 | 3 shards | 1 | About 1.5 minutes of tests per shard. |

Eighteen jobs fewer per full run, and seven fewer per Preflight run. The
longest job of the suite is unchanged: the self-host shards at 10 to 13
minutes. `TestSelfHostConstFuncGen2` is opt-in locally, so the shard job
sets `RUN_CONST_FUNC_GEN2=1`.

## The catch-all lane, measured

The lane's selection after the deletion, run through `cmd/ci-test-workers`
with two workers on the 4-core container beside other work:

| Measure | Value |
| --- | ---: |
| Tests | 1655 |
| Wall time, two workers | 41.7 min |
| Tests at 10 s or more | 64 |
| Their summed duration | 3787 s |

`.github/other-test-weights.txt` lists those 64 tests, and the lane runs
three x86-64 shards instead of four, with two aarch64 shards as before.

## The main ruleset

On 2026-10-25 the owner removed the `main` ruleset. Until then
`strict_required_status_checks_policy` required a branch to be level with
main before it merged. Every merge therefore forced every other open pull
request to rebase and run its suite again. The required checks also named a
job that no longer existed. The `changes` job's identical-tree proof still
applies when a branch is level with main. In every other case the main run
is the backstop for the merged tree.
