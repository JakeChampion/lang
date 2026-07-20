# Deterministic simulation testing (DST) — design brief

From the 2026-07 PLT landscape survey (`PLT-LANDSCAPE-2026.md` §2.4).
Prior art: TigerBeetle's simulator, FoundationDB's deterministic
runtime, Antithesis. The pitch: run concurrent programs under a
virtual clock and simulated I/O, drive every nondeterministic choice
from a seeded RNG, and a concurrency bug becomes a seed you replay —
not a flake you shrug at.

## Why Fern gets this almost free

DST is usually a heroic retrofit because runtimes scatter ambient
syscalls. Fern's async surface has exactly one seam:

- `Future[T]` is a plain library enum (`Ready` / `Pending(token,
  resume)`) — anything can construct one; nothing about it is
  fd-specific except how the combinators wait.
- All waiting funnels through the combinator loops in
  `internal/stdlib/std/async.fern`, which touch precisely four
  builtins: `poll(fds, timeout_ms)`, `monotonic_ns()`,
  `wasm_timer_pollable(ns)`, `wasm_pollable_drop(tok)`.
- `std/mock_platform` already establishes the test-double convention.

Substituting those four calls with a virtual implementation makes
`gather` / `race` / `with_deadline` fully deterministic — on every
backend, including the interpreter, where fd-backed `Pending` futures
today never resolve at all (`ASYNC.md` limitation). DST therefore
also *closes a backend gap*: async programs become testable under
`-interp`, the fastest feedback loop in the toolchain.

## Design

### The driver seam

A `Driver` value bundles the four waiting primitives:

```fern
pub trait Driver {
    function poll_ready(self: Self, toks: i32[], timeout_ms: i32): i32;
    function now_ns(self: Self): i64;
    function timer(self: Self, ns: i64): i32;
    function drop_token(self: Self, tok: i32): i32;
}
```

`std/async` keeps its exact public surface; each combinator gains a
`*_on(drv, …)` sibling carrying the loop body, and the existing
`gather`/`race`/`with_deadline` delegate to it with the **real
driver** (whose methods call the builtins — behaviour-identical,
verified by the existing `async_combinators` suites). The real driver
is the only user of the builtins afterwards; the loops themselves go
driver-generic. One copy of the combinator logic, no drift.

Design choice — `dyn Driver` vs generic `[D: Driver]`: generic. The
combinators are already generic over `T` and monomorphised; adding
`D` keeps static dispatch and avoids committing `dyn` to the hottest
stdlib loops. (`dyn` remains available to a user who wants to pass
drivers around dynamically.)

### The sim driver (`std/sim`)

State: a virtual clock (`i64` ns, starts at 0), a token table
(token → ready-at virtual time, or "ready when fired"), and a seeded
PRNG (`sim.new(seed)` — a small explicit-state LCG/xorshift in Fern;
`std/math.random_int`'s CSPRNG is deliberately NOT used, determinism
is the point).

- `timer(ns)` allocates a token that becomes ready at `now + ns`.
- `sim.future_at(ns, v)` — a `Future[T]` that resolves to `v` at
  virtual time `ns` (token + resume closure returning `Ready(v)`).
- `sim.future_chain(...)` — multi-step pending chains, for testing
  re-suspension shapes like `__fetch_drain`.
- `poll_ready(toks, timeout_ms)`: find the earliest ready-at among
  `toks` (and the timeout, when `>= 0`); **advance the virtual clock
  to it**; return its index (or the timeout sentinel per the real
  `poll` contract). Ties broken by the seeded PRNG — so interleaving
  order is exercised but reproducible from the seed.
- `now_ns()` returns the virtual clock. Time never advances except
  through `poll_ready` — a busy loop that never waits is a hang in
  simulation exactly as in production.

Faithfulness rule: the sim driver must honour the SAME contract the
real `poll` builtin has (return-value meaning, timeout sentinel, the
with_deadline timer-slot convention), pinned by running the portable
combinator test corpus against both drivers and asserting identical
results.

### What slice 1 is (the shippable core)

1. The `Driver` trait + `*_on` refactor in `std/async` (real driver
   extracted, zero behaviour change, existing suites green).
2. `std/sim` with clock, tokens, seeded PRNG, `future_at`/chains.
3. Tests: virtual-time `with_deadline` (winners under the deadline,
   `None` past it — exact, not sleep-flaky); `race` tie broken
   deterministically by seed; `gather` over out-of-order readiness;
   same corpus green under `-interp` and native; a seed-sweep case
   (N seeds, all green) as the property-test seed.

### Later slices (not slice 1)

- **SimNet**: scripted request/response endpoints shaped like
  `fetch_future` (connect/send/readable/chunked reads), so handler
  fan-out logic tests against scripted upstreams with injected
  latency.
- **Fault injection**: seed-driven timeout/error/partial-read
  schedules; the failure report prints the seed; a `--seed` rerun
  replays it.

  **Status: shipped (#5360 slice 3).** Per-endpoint fault modes on
  `sim.Net`, value-returning like `serve`: `fault_fail` (immediate
  `""`, the real connect failure), `fault_stall` (never resolves — a
  never-ready token, dropped by `with_deadline_on` at the exact
  virtual deadline), `fault_partial(k)` (the first `min(k, #chunks)`
  chunks arrive on schedule, then silence — never resolves), and
  `fault_flaky(p)` (wraps the endpoint's fault mode, default fail;
  each fetch consumes exactly one sim-PRNG draw in program order, the
  fault firing on `draw < p`). `sim.sweep_seeds(n, prop)` is the
  seed-replay workflow in miniature (first failing seed, 0 if all
  pass) and `Sim.rng_state()` supports lockstep assertions. Suite:
  `examples/tests/sim_fault_test.fern` (incl. a pinned cross-backend
  flaky(50) golden) with interp/native/wasm gates in
  `internal/e2e/sim_fault_test.go`.
- **fernsmith integration**: random combinator programs × random
  seeds, differential across backends — extends the existing
  numeric-property harness pattern to concurrency.

  **Status: shipped (#5360 slice 4).** Like the numeric-property
  harness (and unlike `internal/fernsmith`, which generates scalar
  control-flow programs with no stdlib surface), the generator is a
  dedicated one in `internal/e2e/sim_property_test.go`: each program
  builds a seeded `Sim` + `Net` with 1–3 scripted endpoints (random
  latencies / chunk schedules, ~half faulted incl. flaky with random
  p), runs 1–3 random `gather_on` / `race_on` / `with_deadline_on`
  stages over mixes of `future_at` / `future_chain` / `fetch_future`,
  and prints a digest of everything observable (results, winner
  indices, `None` slots, final `now_ns`, `rng_state`, per-endpoint
  hits). Interp, native x86-64, and wasm must produce byte-identical
  stdout — any divergence is a miscompile or a nondeterminism leak,
  and the failing subtest prints the whole program for replay.
  `TestSimProperty` is the bounded 25-seed CI sweep,
  `TestSimProperty_Regressions` pins four generated programs
  verbatim, `FuzzSimProperty` is the deeper search entry point.
- **Platform alignment**: when `PLATFORM-RESEARCH.md` Rec §1 lands a
  `Platform` capability bag, the sim driver becomes the async face of
  the mock platform (Rec §6) rather than a standalone object.

## Constraints

- Zero cost and zero behaviour change on the real path (the real
  driver inlines to exactly today's builtin calls; monomorphised).
- No backend or IR changes; no component-model surface — this is
  pure stdlib + tests, disjoint from the active #4315–#4320 lane.
- Pure Fern, so it runs identically on interp / native / wasm; the
  sim never calls a nondeterministic builtin.
- Test-runner rule applies: every sim assertion helper added to
  `std/test`-adjacent code ships with pass + fail path coverage.
