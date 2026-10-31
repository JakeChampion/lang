# Deterministic simulation testing (DST) — design brief

Status: [reference] design of deterministic simulation testing (`std/sim`,
`std/sim_platform`, `std/sim_fetch`); its slices have shipped.

From the 2026-07 PLT landscape survey (`PLT-LANDSCAPE-2026.md` §2.4).
Prior art: TigerBeetle's simulator, FoundationDB's deterministic
runtime, Antithesis. The pitch: run concurrent programs under a
virtual clock and simulated I/O, drive every nondeterministic choice
from a seeded RNG, and a concurrency bug becomes a seed you replay —
not a flake you shrug at.

## Why Fern gets this almost free

DST is usually a large retrofit because runtimes call the OS from many
places. Fern's async surface has two seams, and neither reads a clock
of its own:

- A wait is a value: `async.Wait { set, until }`, where `until` is an
  instant on the clock of whatever waits. A `Future[T]` is a plain
  library enum (`Ready`, `Pending(Wait, resume)`, `Cancelled`) that
  anything can construct.
- The combinators `gather`, `race` and `with_deadline` only build
  futures. The code that drives a future, or the scheduler that resumes
  a parked task, decides what "now" is.

So a simulation supplies the clock where a program would supply the
machine's clock, and the same combinators and tasks run on it. This
also closes a backend gap: `sim.block_on` needs no task support, so
async programs are testable under `-interp`, the fastest feedback loop
in the toolchain.

## Design

The design as shipped is in `docs/ASYNC.md` sections 6, 7 and 9. In
short:

- `sim.Sim` holds a virtual clock (`i64` ns, starts at 0) and a seeded
  PRNG (`sim.new(seed)`; `std/math.random_int`'s CSPRNG is
  deliberately not used, because determinism is the point).
- `sim.block_on(d, f)` drives a future with no task. It moves the clock
  to each deadline that `f` waits for and resumes `f` with
  `Wake.TimedOut`.
- `sim.run_tasks(d, entries, cancel_at)` is a scheduler. It resumes the
  task whose wait is due soonest, moves the clock to that instant, and
  the seed breaks a tie between tasks. `cancel_at` scripts a
  cancellation at a virtual instant.
- `sim.future_at(at, v)` and `sim.future_chain(at, step, n, v)` are
  futures that are ready at virtual instants, for tests of the
  combinators.
- Time never moves except when the simulation drives a wait, so a busy
  loop that never waits is a hang in simulation as it is in production.
- `sim.Sim` also implements `async.Driver`, the reactor that the
  `std/serve` loop watches descriptors with, and scripts readiness with
  `ready_at`.

### What slice 1 was

The first slice put a driver seam (`*_on` siblings of each combinator)
into `std/async`, and `std/sim` with its clock, tokens and seeded PRNG.
The seam is gone: deadlines are now instants, and the code that waits
supplies the clock, as the section above describes.

### Later slices (not slice 1)

- **SimNet**: scripted upstreams for the real client — `std/sim_fetch`'s
  `Net` is a `fetch.Transport` whose routes answer with injected
  latency and chunking — so handler fan-out logic, run as tasks under
  `sim.run_tasks`, tests against them in virtual time.
- **Fault injection**: seed-driven timeout/error/partial-read
  schedules; the failure report prints the seed; a `--seed` rerun
  replays it.

  **Status: shipped.** Faults are scripted answers on `sim_fetch.Net`
  routes: `reset()` (the connection closes with nothing sent),
  `silent()` (never answers; the client's timeout or a cancellation
  ends the fetch) and `in_chunks(size, every)` with `held()` (a
  prefix, then silence). `sim.sweep_seeds(n, prop)` is the seed-replay
  workflow in miniature (first failing seed, 0 if all pass) and
  `Sim.rng_state()` supports lockstep assertions. Suites:
  `tests/stdlib/sim_fetch_test.fern`, `tests/stdlib/sim_platform_test.fern`
  and `tests/stdlib/sim_driver_test.fern` (incl. a pinned PRNG golden).
- **fernsmith integration**: random combinator programs × random
  seeds, differential across backends — extends the existing
  numeric-property harness pattern to concurrency.

  **Status: shipped (#5360 slice 4).** Like the numeric-property
  harness (and unlike `internal/testing/fernsmith`, which generates scalar
  control-flow programs with no stdlib surface), the generator is a
  dedicated one in `internal/testing/e2e/sim_property_test.go`: each program
  builds a seeded `Sim` with 1–3 scripted upstream futures (random
  latencies / chunk schedules, ~half faulted — fail, stall or partial,
  some flaky with random p drawn from the sim PRNG), runs 1–3 random
  `gather` / `race` / `with_deadline` stages over mixes of
  `future_at` / `future_chain` / those upstreams, driven by
  `sim.block_on`, and prints a digest
  of everything observable (results, winner indices, `None` slots,
  final virtual clock, `rng_state`). Interp, native x86-64, and wasm must produce byte-identical
  stdout — any divergence is a miscompile or a nondeterminism leak,
  and the failing subtest prints the whole program for replay.
  `TestSimProperty` is the bounded 25-seed CI sweep,
  `TestSimProperty_Regressions` pins four generated programs
  verbatim, `FuzzSimProperty` is the deeper search entry point.
- **Platform alignment**: `std/sim_platform` is a `platform.Platform`
  over the simulation, the async face of the mock platform: the sim's
  clock and PRNG, the scripted network, and the mock's call log
  (`internal/testing/e2e/sim_platform_test.go`).

## Constraints

- Zero cost and zero behaviour change on the real path (the real
  driver inlines to exactly today's builtin calls; monomorphised).
- No backend or IR changes; no component-model surface — this is
  pure stdlib + tests, disjoint from the active #4315–#4320 lane.
- Pure Fern, so it runs identically on interp / native / wasm; the
  sim never calls a nondeterministic builtin.
- Test-runner rule applies: every sim assertion helper added to
  `std/test`-adjacent code ships with pass + fail path coverage.
