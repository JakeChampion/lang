# Async & concurrency in Fern (user guide)

Status: [reference] user guide to the shipped `std/async` combinator surface.

Fern's concurrency is **colorless**: no function coloring in the surface you
write. Concurrency is expressed with plain library combinators — `gather` /
`race` / `with_deadline` — over a `Future[T]` (a not-yet-ready value). You write
straight-line code and make the concurrency points **explicit** at the
`gather`/`race` call. No stackful green threads, no parse-time CPS transform —
just functions and values.

The `async function` modifier this guide's own examples use is **not** function
coloring: it colors a component-model **export**, not a call graph. It marks a
function for the WASI Preview-3 `canon lift async` surface and changes nothing
about how the body is written, type-checked, or run — the interpreter executes
an `async function` exactly as a plain one. See `docs/WASI-PREVIEW3-ASYNC-PLAN.md`.

This is the user-facing guide. For the design and internals see
`docs/ASYNC-REDESIGN.md` (the model + rationale), `docs/STREAM-TYPE-SURFACE.md`,
and `docs/WASI-PREVIEW3-ASYNC-PLAN.md`.

> Backend support: the combinator surface (`std/async`) drives REAL overlapping socket
> I/O on the native backends (x86-64 / arm64) via the OS reactor; the portable
> `Ready`-future path runs on every backend (interp / wasm / native). The WASI
> Preview-3 `Stream[T]` import surface below does real wire I/O on wasm. The next
> milestone is promoting `Future[T]` to a first-class IR type so wasm futures use
> the component-model-async host scheduler (`docs/ASYNC-REDESIGN.md` PR5).

The patterns shown here are exercised by `tests/stdlib/async_combinators_test.fern`
and the Go e2e suite (`async_combinators_test.go`, `async_task_fetch_test.go`).

---

## 1. Streams: `Stream[T]`

An async host import can return a `Stream[T]` — a sequence delivered incrementally
over the wire. Under the colorless model the call site sees a plain value:

```fern
@import("wasi:http/types", "body-stream")
async function body(): Stream[u8];
```

**Collect it eagerly** — the whole stream into a `u8[]`:

```fern
async function handle(): i32 {
    let bytes: u8[] = body();   // drains the stream to EOF, returns the array
    return bytes.len();
}
```

**Iterate it lazily** — one element at a time, off the wire, before EOF (O(1)
memory, no full buffer):

```fern
async function sum(): i32 {
    let total = 0;
    for x in body() {           // pulls one element per turn
        total = total + (x as i32);
    }
    return total;
}
```

Lazy `for x in stream` works for any scalar element type (`u8` / `i32` / `i64` /
`f64`, …). A `Stream[T]` **parameter** is the mirror: you pass an eager `T[]` and
the wrapper streams its elements out over the wire.

---

## 2. `Future[T]` — a not-yet-ready value

`std/async` defines:

```fern
pub enum Future[T] {
    Ready(T),                          // already resolved
    Pending(i32, (i32) => Future[T]),  // suspended on an fd; resume(fd) -> next
}
```

A `Pending` future wraps one wait of your own — a socket read, a timer — and
resolves when its file descriptor becomes readable, driven by the OS `poll` on
native. `Ready(v)` is a value that's already available (and it resolves on every
backend, including interp / wasm). HTTP is not a future: `fetch.send` waits on
DNS, the connect, TLS and each read under its own timeouts, and runs
concurrently under the task combinators instead (§7).

`Future[T]` is **generic over `T`** — `Future[string]`, `Future[i32]`, etc.

---

## 3. `gather` — fan out and await all

`gather` waits on every future at once and returns all results in input
order.

```fern
import "std/async";

// later resolves to v once its timer fires.
function later(ms: i32, v: i32): async.Future[i32] {
    return Pending(timer_fd(ms), (woken: i32) => mk(v));
}

function mk(v: i32): async.Future[i32] {
    return Ready(v);
}

function main(): i32 {
    let got: i32[] = async.gather([later(60, 4), later(20, 5)], 0 - 1);
    return got[0] + got[1];       // 9, after ~60ms: both timers waited at once
}
```

`gather(fs, on_incomplete)` — the `on_incomplete` value fills any slot whose
future never resolves (a `poll` error, or the `-1` `poll` stub on interp/wasm).
With a blocking native `poll`, live futures always make progress.

---

## 4. `race` — first to finish wins

`race` runs the futures until the **first** resolves, returns `(winnerIndex,
value)`, and cancels the rest (happy-eyeballs / first-wins). A loser is
resumed once more, with `async.cancelled()`, and releases what it holds: a
future over a socket closes it. A future of your own handles `cancelled()` by
cleaning up and returning `Ready` without waiting on anything. Under a task, a
wait in that path could rewind into it and release the same resource twice.

```fern
let (winner, v) = async.race([later(400, 1), later(20, 2)], 0 - 1);
// `winner` is the index that finished first (1); `v` its result (2).
```

(It's spelled `race`; the older `race { … }` keyword block was removed in favour
of this combinator.)

---

## 5. `with_deadline` — await all within a budget

`with_deadline(deadline, fs)` is `gather` with an SLA: fan out, take
whatever answers within `deadline` (a `Duration`, built with
`time.duration_millis` and friends) of wall-clock time, and drop the
stragglers (their slots come back `None`).

```fern
let got: Option[i32][] = async.with_deadline(time.duration_millis(250), [
    later(20, 1),
    later(900, 2),
]);   // [Some(1), None]: the 900ms timer missed the budget
```

---

## 6. Deterministic simulation: the `Driver` seam and `std/sim`

Everything nondeterministic the combinators do funnels through one seam: the
`async.Driver` trait (`poll_ready` / `now_ns` / `timer` / `drop_token`). Each
combinator has an `*_on(drv, …)` sibling — `gather_on` / `race_on` /
`with_deadline_on` — carrying the loop; the plain forms delegate to it with the
real driver (whose methods are exactly the `poll` / `monotonic_ns` /
`wasm_timer_pollable` / `wasm_pollable_drop` builtins).

`std/sim` substitutes a virtual driver (`docs/DST-PLATFORM-BRIEF.md`): a
virtual clock starting at 0 ns that advances only inside `poll_ready`, tokens
that encode their ready-at time, and a seeded PRNG breaking readiness ties —
so a whole fan-out is a pure function of the seed, on every backend
**including the interpreter** (where fd-backed futures never resolve on the
real driver). Deadlines become exact virtual-time assertions instead of
sleeps:

```fern
import "std/async";
import "std/sim";

let d: sim.Sim = sim.new(7);
let fs: async.Future[string][] = [
    sim.future_at(d, 40000000, "late"),   // ready at 40ms of virtual time
    sim.future_at(d, 10000000, "early")   // ready at 10ms
];
let got: Option[string][] = async.with_deadline_on(d, time.duration_millis(25), fs);
// got == [None, Some("early")], and d.now_ns() == exactly 25000000
```

`sim.future_at(d, at_ns, v)` resolves to `v` at virtual time `at_ns`;
`sim.future_chain(d, at_ns, step_ns, n, v)` re-suspends `n` times (the
`__fetch_drain` shape) before resolving. See
`tests/stdlib/sim_driver_test.fern`.

### Scripted upstreams: `std/sim_fetch`

`sim_fetch.Net` is a `fetch.Transport` under the sim clock: names, listeners
and routes whose scripted answers carry a first-byte latency, a chunking
schedule and a close policy. The real client runs against it unchanged
(`fetch.send_on`, or `plat.http` over a `sim_platform.SimPlatform`), and
`sim.run_tasks(drv, entries, cancel_at_ms)` (§8) drives a fan-out of such
fetches in virtual time.

```fern
// UP is the upstream's address; fetch_via(plat, path) answers the body of
// plat.http(fetch.get("http://up.test" + path)).
let d: sim.Sim = sim.new(1);
let n: sim_fetch.Net = sim_fetch.net(d).host("up.test", [UP]).listen(UP, 80, 5)
    .route(UP, 80, "/a", [sim_fetch.reply(200, "alpha").after(30)])
    .route(UP, 80, "/b", [sim_fetch.reply(200, "beta").after(10)]);
let plat: sim_platform.SimPlatform = sim_platform.new(d, n);
let entries: (() => string)[] = [() => fetch_via(plat, "/a"), () => fetch_via(plat, "/b")];
let got: async.TaskStatus[string][] = sim.run_tasks(d, entries, [0 - 1, 0 - 1]);
// /b answers at 15ms of virtual time and /a at 35ms
```

Faults are scripted answers: `sim_fetch.reset()` closes the connection with
nothing sent, `silent()` never answers (the client's timeout or a
cancellation ends the fetch), and `in_chunks(size, every_ms)` with `held()`
delivers a prefix and then goes quiet. `sim.sweep_seeds(n, prop)` runs
`prop(seed)` over seeds `1..n` and returns the first failing seed (0 if all
pass), with `Sim.rng_state()` available for lockstep assertions. See
`tests/stdlib/sim_fetch_test.fern` and `tests/stdlib/sim_platform_test.fern`.

That purity claim is itself property-tested: the harness in
`internal/testing/e2e/sim_property_test.go` generates random sim programs —
scripted upstream futures with random latencies, chunk schedules, and
faults (fail, stall, partial, and flaky ones that draw on the sim PRNG),
driven through random `gather_on` / `race_on` / `with_deadline_on`
pipelines — and requires interp, native x86-64, and wasm to print
byte-identical digests (results, winners, `None` slots, `now_ns`,
`rng_state`). Any backend divergence on a sim
program is a real bug, never a flake; a failure prints the whole
program and its generator seed for replay
(`TestSimProperty` / `TestSimProperty_Regressions` /
`FuzzSimProperty`).

## 7. HTTP fan-out: `fetch.send` under the task combinators

There is one HTTP client, `fetch.send`, and it is how concurrent fetches are
written too. Each entry of `async.gather_tasks` / `race_tasks` /
`with_deadline_tasks` runs as a task, the client's waits park it (§8), and
the combinator waits on all of them at once:

```fern
import "std/async";
import "std/fetch";

function status_of(url: string): i32 {
    match (fetch.send(fetch.get(url))) {
        Ok(resp) => { return resp.status; },
        Err(e) => { return 0 - 1; }
    }
    return 0 - 1;
}

function main(): i32 {
    let both: (() => i32)[] = [
        () => status_of("http://cache.internal/key"),
        () => status_of("http://primary.internal/key"),
    ];
    let statuses: i32[] = async.gather_tasks(both, 0 - 2);
    return statuses.len();        // both fetches overlapped on one thread
}
```

Every fetch keeps the whole client: DNS, TLS, redirects, the request's
timeouts, content decoding, a `Host` header naming the URL's authority, and a
typed `FetchError` for each failure. `race_tasks` answers the first to finish
and cancels the rest, whose fetches end as `FetchError.Cancelled` after
closing their sockets.

---

## 8. Tasks: parking a call chain

`async.wait_any(set, timeout_ms)` waits for any of the (fd, interest) pairs
in `set` (1 readable, 2 writable), bounded by `timeout_ms` (-1 for none),
and answers the index of the pair that became ready or -1 at the timeout.
With no task current it blocks: one `poll` for a read-only set, a reactor
made for the call otherwise. Under a scheduler it *parks*: the compiler
lowers every function that reaches the park to a resumable form, so
the whole call chain above the wait saves its live locals and returns to the
scheduler, and runs on from the wait when the scheduler resumes it. Nothing
is written at the call site: a function that calls `wait_any`, or calls one
that does, is lowered that way by reachability (`compiler/suspend.fern`).
The fetch client's waits are such calls: `net.read_deadline`, the DNS
exchange's receive waits and `dns.connect_race`'s connect wait all go
through `wait_any`, so a `fetch.send` inside a task parks at each of them.

```fern
let t: async.Task[i32] = async.task_new(() => handler());
let st: async.TaskStatus[i32] = async.task_start(t);
match (st) {
    Done(v) => { … },
    Suspended(w) => {
        // w.set and w.timeout_ms are what the task waits for; here the
        // scheduler waits on them itself.
        let woke: i32 = async.wait_any(w.set, w.timeout_ms);
        st = async.task_resume(t, woke);
    },
    Cancelled => { … }
}
async.task_free(t);
```

`task_resume(t, woke)` makes the parked `wait_any` answer `woke`;
`task_cancel(t)` runs the task on with every wait answering
`async.cancelled()` and never parking again, which is how a disconnected
client's handler winds down through its own control flow (`defer`s
included). The interpreter keeps the blocking fallback: no task is ever
current, so `task_start` runs its entry to completion and both paths behave
as a plain program does. `std/serve`'s stateless loop is the scheduler for
handlers: a handler that parks becomes a flight the loop watches on its
reactor, and the loop serves other connections until the flight runs on
and answers.

Inside a task, fan out with the task combinators. Each runs its entries as
tasks of the calling task and parks on the union of their waits, so a
handler that fetches from two upstreams parks once for both (once #12456
lets a handler call them):

```fern
let both: (() => Result[HttpResponse, fetch.FetchError])[] = [() => plat.http(a), () => plat.http(b)];
let rs: Result[HttpResponse, fetch.FetchError][] = async.gather_tasks(both, fallback);
let (won, first) = async.race_tasks(both, fallback);
let timed: Option[Result[HttpResponse, fetch.FetchError]][] = async.with_deadline_tasks(time.duration_millis(200), both);
```

`gather_tasks(entries, on_incomplete)` answers every result in order;
`race_tasks(entries, none_val)` answers `(index, value)` of the first to
finish and cancels the rest; `with_deadline_tasks(deadline, entries)`
answers `Some(value)` for each that finished in time and `None` for each
cancelled at the deadline. A cancelled child is not abandoned: `task_cancel`
runs it on with its waits answering `cancelled()`, so it leaves through its
own exit paths and its `defer`s run, and a task that is itself cancelled
cancels its children the same way. The `Future` combinators (`gather`,
`race`, `with_deadline`, §3) park too when called inside a task, since the
driver's wait is a `wait_any`. With no task current a combinator's own wait
blocks; under the blocking fallback each entry runs to its end inside
`task_start`, in order, so the first entry wins every race.
A parked frame keeps what it holds: a `str` or byte view read after the
park still sees its owner, and a closure sharing a mutated scalar with the
frame still shares it (`TestSelfHostTaskFrame`).
The simulation drives tasks too: `sim.run_tasks(drv, entries, cancel_at_ms)`
runs its entries as tasks in virtual time, each move of the clock
(`Sim.advance_to`, a `poll_ready`, any `wait_any`) a park the driver answers
when that moment is the earliest any task is due, and `cancel_at_ms[i]` is
the virtual time task `i` is cancelled at, a scripted disconnect. A handler's
`plat.http` over a `SimPlatform` parks on the scripted network's latencies
and, cancelled while it waits, answers `FetchError.Cancelled` — the same bytes
on every target (`TestSelfHostSimTasks`).
A streamed request body parks the same way: with `serve.Config.stream_bodies`
a handler starts on its request's header block, and `req.body_stream()`
pulls the bytes as the handler reads them, each pull a `wait_any` on the
connection under the body's data-rate deadline (`std/stream`'s source,
`docs/NET-P3-SUSPENSION-PLAN.md` §3.9). The pull is a closure the stream
calls, so this is the indirect call that reaches a park.
`docs/NET-P3-SUSPENSION-PLAN.md` is the design.

## 9. How it works (one paragraph)

A `Future[T]` is either a ready value or an fd plus a continuation. The
combinators gather the pending futures' fds, block once in the universal `poll`
builtin (poll(2) / ppoll(2) on native; a `-1` stub on interp/wasm), resume the
future whose fd is ready (its continuation does the actual non-blocking read),
and repeat until done. One thread, overlapping I/O, no function coloring, no
green-thread stacks, no compiler transform — it rides enums, closures, and loops
that every backend already lowers. See `docs/ASYNC-REDESIGN.md`.

## Current limitations

- `Pending` futures resolve on **native** (fd-backed, via poll(2)/ppoll(2)),
  on **wasm** (pollable-backed, via wasi:io/poll — `poll` forwards to it) and
  on the **interpreter** (a reactor over the handles). The wait token is
  portable: `tcp_pollable(c)` is the raw fd on native and a real wasi:io/poll
  pollable handle on wasm, so a future over a socket overlaps under `gather`
  everywhere.
- A task parks only in compiled code (§8), on x86-64, arm64 and wasm alike.
  Under the interpreter the task combinators and `sim.run_tasks` run their
  entries one after another.
- A task cancelled while the fetch client resolves a name sees the resolver's
  `NoReply` (as `FetchError.Dns`), not `Cancelled`: the DNS exchange's waits
  answer `cancelled()` as a passed deadline. Its connect and read waits
  report the cancellation (`net.Abandoned`, `net.Interrupted`), which
  the client maps to `FetchError.Cancelled`.
- `with_deadline` enforces its deadline on **both** native and wasm: native via
  `poll(2)`'s timeout arg; wasm by appending a real timer pollable
  (`monotonic-clock` `subscribe-duration`) to the poll set each round, so the
  timer firing is the deadline. (This needed the composer to export both `now`
  and `subscribe-duration` on one `monotonic-clock` import instance — see
  `docs/ASYNC-FUTURE-UNIFICATION.md`.)
- `race`, `with_deadline`, and `gather` when its wait fails cancel every
  future they abandon (`__drop_losers`): the future's pollable is dropped
  first, since on wasm it is a child of its socket and wasmtime traps with
  "resource has children" otherwise, and then its continuation is resumed with
  `cancelled()` and closes the socket.
- `std/async` is now the **single** reactor — the legacy `std/task`, `std/reactor`,
  and `std/wasm_reactor` modules it was distilled from have all been deleted.
  `gather` / `race` / `with_deadline` over `Future[T]` cover the native (fd) and
  wasm (pollable) paths alike.
