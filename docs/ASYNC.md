# Async and concurrency in Fern (user guide)

Status: [reference] user guide to the shipped `std/async` surface: futures, tasks and their schedulers.

Fern has no function colouring. A function that waits is written and
called like any other function. Concurrency comes from two library
concepts in `std/async`:

- A **task** is a call chain that can park. When code in a task reaches a
  wait, the whole chain keeps its place and returns to the scheduler of
  the task. The scheduler resumes the chain when the wait is answered.
- A **future** is a value that is ready, pending on a wait, or cancelled.
  The combinators `gather`, `race` and `with_deadline` build one future
  from many. `wait` and `block_on` drive a future to its end.

This guide describes both, and the schedulers that drive them: the
`std/serve` loop, `block_on`, `sim.block_on` and `sim.run_tasks`. The
design records are `docs/NET-P3-SUSPENSION-PLAN.md` (tasks) and
`docs/DST-PLATFORM-BRIEF.md` (the simulation).

`tests/stdlib/async_combinators_test.fern`, `tests/stdlib/sim_driver_test.fern`,
and the e2e tests `TestSelfHostTaskCombinators` and `TestSelfHostSimTasks`
run the patterns in this guide.

---

## 1. The `async` export modifier is a different thing

The `async` keyword before `function` marks a WebAssembly component
export for the WASI Preview 3 `canon lift async` surface
(`docs/WASI-PREVIEW3-ASYNC-PLAN.md`). It does not make a function a task
or give it a future. It changes nothing about how the body is written,
type-checked or run. The interpreter runs an `async function` as a plain
function. The examples in section 2 use it because they are component
exports.

## 2. Streams: `Stream[T]`

An async host import can return a `Stream[T]`, a sequence that arrives
over the wire in parts. The call site sees a plain value:

```fern
@import("wasi:http/types", "body-stream")
async function body(): Stream[u8];
```

**Collect it eagerly.** The whole stream goes into a `u8[]`:

```fern
async function handle(): i32 {
    let bytes: u8[] = body();   // reads the stream to EOF, returns the array
    return bytes.len();
}
```

**Iterate it lazily.** The loop pulls one element at a time off the
wire, before EOF, with no full buffer:

```fern
async function sum(): i32 {
    let total = 0;
    for x in body() {           // pulls one element per turn
        total = total + (x as i32);
    }
    return total;
}
```

Lazy `for x in stream` works for any scalar element type (`u8`, `i32`,
`i64`, `f64`). A `Stream[T]` **parameter** is the mirror: you pass a
`T[]`, and the wrapper sends its elements out over the wire.

---

## 3. Waits and wakes

Every wait in Fern is one value:

```fern
pub struct Wait { set: i32[], until: Option[Instant] }

pub enum Wake { Woke(i32), TimedOut, Cancelled }
```

`set` holds (fd, interest) pairs: interest 1 is readable and 2 is
writable. `until` is a deadline: an **instant on the clock of whatever
waits**, not a duration. A wait ends with a `Wake`: `Woke(i)` names the
pair that became ready, `TimedOut` says that the clock reached `until`,
and `Cancelled` says that the waiting task was cancelled.

`async.wait_any(set, until)` is the leaf wait that sockets, DNS, TLS and
`fetch` use. Inside a task it parks the task. Outside a task it blocks:
one `poll` for a read-only set, or a reactor made for the call, and the
machine's monotonic clock decides `until`. Nothing outside a task
answers `Cancelled`.

A deadline is an instant. So only the code that makes a deadline from a
duration reads a clock, and it reads the clock that it was given.
A handler takes the instant from its platform (`plat.instant()`, or the
request's `plat.deadline()`). A plain program takes it from
`clock.instant()`. A simulated program takes it from the `sim.Sim`
(`d.now()`, `sim.at(offset)`). The combinators and the schedulers do not
convert durations, so the same code runs on the real clock and on the
virtual one.

## 4. `Future[T]`

```fern
pub enum Future[T] {
    Ready(T),
    Pending(Wait, (Wake) => Future[T]),
    Cancelled,
}
```

A `Pending` future holds its wait and a continuation. Whatever drives
the future waits on the `Wait` and calls the continuation with the
`Wake` that ended it. The continuation gives the next state. A future of
your own over a socket looks like this:

```fern
function readable(fd: i32, v: i32): async.Future[i32] {
    let set: i32[] = [fd, 1];
    return Pending(async.Wait { set: set, until: None }, (wake: async.Wake): async.Future[i32] => Ready(v));
}
```

A pending future that is resumed with `Cancelled` releases what it holds,
for example it closes its socket. What it gives back then is not used.
`Cancelled` as a state is a future that ended without a value.

`async.spawn(entry)` makes a task into a future. It starts `entry` as a
task at once. The future is ready with the task's result when the task
finishes, and pending on the task's wait while the task is parked. When
the spawned future is resumed with `Cancelled`, the task runs on to its
end, as section 7 describes, and the future ends cancelled.

## 5. The combinators

The combinators build a future from futures. They do not wait and they
read no clock. The wait of a combined future is the union of the pending
inputs' waits: every pair of each input, in input order, and the soonest
of their deadlines. A wake goes to the input that owns the pair, and a
timeout goes to each input whose deadline is that soonest one.

| Combinator | Is ready with |
|---|---|
| `async.gather(fs)` | every value, in input order. It ends cancelled when one input ends cancelled, and cancels the others. |
| `async.race(fs)` | `(index, value)` of the first input to be ready; a tie goes to the lower index. It cancels the others, and ends cancelled when every input ends cancelled (so for no input). |
| `async.with_deadline(until, fs)` | `Some(value)` for each input that is ready by the instant `until`, and `None` for each other input. It cancels the inputs that are pending at `until`. A deadline equal to an input's own deadline wins. |

When a combined future is resumed with `Cancelled`, it cancels every
pending input.

## 6. Driving a future

Three functions drive a future to its end. Each answers `Option[T]`:
`Some` with the value, or `None` when the future ends cancelled.

- **`async.wait(f)`** parks the task that calls it on the scheduler of
  that task: the `std/serve` loop, `sim.run_tasks`, or a combinator's
  own wait. It reaches no clock and no reactor, so a handler can call it
  (E080 does not refuse it).
- **`async.block_on(f)`** waits on the machine's reactor and monotonic
  clock when no task is current, and parks as `wait` does inside one.
  This is the call for a program's `main`. E080 refuses it in a
  handler, because it reaches the reactor.
- **`sim.block_on(d, f)`** drives `f` on the virtual clock of the
  `sim.Sim` `d`, with no task. The clock moves to each deadline that
  `f` waits for. This path needs no task support, so the interpreter
  gives the same result.

`wait` answers `None` when the task that calls it is cancelled. With no
task current, `wait` cannot park, so it cancels a pending `f` and
answers `None`. Under `sim.block_on`, a wait with no deadline never
ends, so `sim.block_on` cancels `f` and answers `None`.

A plain program that fetches two URLs at once:

```fern
import "std/async";
import "std/fetch";

function status_of(url: string): i32 {
    match (fetch.send(fetch.get(url))) {
        Ok(resp) => { return resp.status; },
        Err(e) => { return 0 - 1; }
    }
}

function main(): i32 {
    let both: async.Future[i32][] = [
        async.spawn(() => status_of("http://cache.internal/key")),
        async.spawn(() => status_of("http://primary.internal/key")),
    ];
    match (async.block_on(async.gather(both))) {
        Some(statuses) => { return statuses.len(); },   // both fetches overlapped on one thread
        None => { return 0 - 2; }
    }
}
```

Each fetch keeps the whole client: DNS, TLS, redirects, the request's
timeouts, content decoding, and a typed `FetchError` for each failure.
A fetch that `race` cancels closes its socket and answers
`FetchError.Cancelled`.

## 7. Tasks and schedulers

A function that calls `wait_any`, or calls a function that does, is
lowered to a resumable form by reachability (`compiler/suspend.fern`).
Nothing is written at the call site. When such a function parks, the
whole call chain above the wait keeps its live locals and returns to
the scheduler. When the scheduler resumes the task, the chain runs on
from the wait.

A parked frame keeps what it holds. A `str` or byte view
read after the park still sees its owner. A closure that shares a
changed scalar with the frame still shares it (`TestSelfHostTaskFrame`).

The scheduler side of `std/async`:

```fern
pub enum TaskStatus[T] { Done(T), Suspended(Wait) }

let t: async.Task[i32] = async.task_new(() => handler());
let st: async.TaskStatus[i32] = async.task_start(t);
while (true) {
    match (st) {
        Done(v) => { break; },
        Suspended(w) => {
            // This scheduler waits on w itself; serve watches w.set on its
            // reactor and compares w.until with its clock.
            st = async.task_resume(t, async.wait_any(w.set, w.until));
        }
    }
}
```

`task_resume(t, wake)` makes the parked `wait_any` answer `wake`. A task
that finishes releases its runtime record, so a scheduler only drops its
handle. `async.call_start(f, a, b)` is the same for a two-argument
function: it answers `Returned(v)` with no task record when `f` does not
park, or `Parked(call, w)`, which the scheduler resumes with
`call_resume`. `std/serve` holds a parked handler this way.

**Cancellation.** `task_resume(t, Wake.Cancelled)` runs the task on with
every wait answering `Cancelled`, and no wait parks again. So a
cancelled task leaves through its own control flow, its `defer`s run,
and the scheduler always gets `Done`. A disconnected client's handler
winds down this way. `async.task_cancelled()` tells code in a task that
the task is cancelled.

**The interpreter** has no tasks. No task is ever current, so
`task_start` runs its entry to completion, `spawn` gives a ready future,
and `wait_any` blocks. A future that must wait under `async.wait` gives
`None` there. `sim.block_on` and `block_on` behave as they do in
compiled code.

## 8. Fan-out in a handler

A handler takes its effects from its platform (E080), and its scheduler
is the serve loop. So it starts each upstream call as a task, combines
the futures, and waits with `async.wait`. It parks once for all of them:

```fern
import "std/async";
import "std/fetch";
import "std/http";
import "std/platform";
import "std/serve";
import "std/time";

function handle(req: HttpRequest, plat: platform.Platform): HttpResponse {
    let calls: async.Future[Result[HttpResponse, fetch.FetchError]][] = [
        async.spawn(() => plat.http(fetch.get("https://example.com/"))),
        async.spawn(() => plat.http(fetch.get("https://example.org/"))),
    ];
    let until: Instant = plat.instant().add(time.duration_millis(200));
    match (async.wait(async.with_deadline(until, calls))) {
        Some(answers) => {
            let n: i32 = 0;
            for a in answers {
                if (a.is_some()) {
                    n = n + 1;
                }
            }
            return http.ok(n.to_string() + " answered in time");
        },
        None => { return http.text(503, "cancelled"); }
    }
}
```

`plat.deadline()`, when it is set, is the request's own deadline as an
instant, so `async.with_deadline(at, calls)` can take it directly. Under
a `sim_platform.SimPlatform` and `sim.run_tasks` the same handler parks
on the scripted network's latencies, and its deadline is an instant on
the virtual clock. The run is then the same on every target.

## 9. Deterministic simulation: `std/sim`

`std/sim` gives a virtual clock that starts at 0 ns and moves only when
the simulation drives a wait, and a seeded generator
(`docs/DST-PLATFORM-BRIEF.md`). A whole run is then a function of the
seed, on every target. Deadlines become exact virtual-time assertions in
place of sleeps:

```fern
import "std/async";
import "std/sim";
import "std/time";

let d: sim.Sim = sim.new(7);
let fs: async.Future[string][] = [
    sim.future_at(sim.at(time.duration_millis(40)), "late"),    // ready at 40 ms of virtual time
    sim.future_at(sim.at(time.duration_millis(10)), "early"),   // ready at 10 ms
];
let got: Option[Option[string][]] = sim.block_on(d, async.with_deadline(sim.at(time.duration_millis(25)), fs));
// got == Some([None, Some("early")]), and d.elapsed() is exactly 25 ms
```

`sim.at(offset)` is the virtual `Instant` `offset` after the simulation
starts. `sim.future_at(at, v)` is ready with `v` at that instant.
`sim.future_chain(at, step, n, v)` waits `n` more times, `step` apart,
before it is ready: the shape of a read loop that parks on each chunk.

`sim.run_tasks(d, entries, cancel_at)` runs its entries as tasks in
virtual time. It resumes the task whose wait is due soonest, and the seed
breaks a tie between tasks.

A task waits for a deadline or a
cancellation: a pair in a wait set never becomes ready under the
simulation. `cancel_at[i]`, when it is `Some`, is the virtual instant at
which task `i` is cancelled: a scripted disconnect. `Sim.advance_to(at)`
inside a task parks until `at`. Code in a task that calls `async.wait`
parks on `run_tasks` the same way.

### Scripted upstreams: `std/sim_fetch`

`sim_fetch.Net` is a `fetch.Transport` on the sim clock: names,
listeners and routes whose scripted answers carry a first-byte latency, a
chunk schedule and a close policy. The real client runs against it
unchanged (`fetch.send_on`, or `plat.http` over a
`sim_platform.SimPlatform`):

```fern
// UP is the upstream's address; fetch_via(plat, path) answers the body of
// plat.http(fetch.get("http://up.test" + path)).
let d: sim.Sim = sim.new(1);
let n: sim_fetch.Net = sim_fetch.new(d).host("up.test", [UP]).listen(UP, 80, time.duration_millis(5))
    .route(UP, 80, "/a", [sim_fetch.reply(200, "alpha").after(time.duration_millis(30))])
    .route(UP, 80, "/b", [sim_fetch.reply(200, "beta").after(time.duration_millis(10))]);
let plat: sim_platform.SimPlatform = sim_platform.new(d, n);
let entries: (() => string)[] = [() => fetch_via(plat, "/a"), () => fetch_via(plat, "/b")];
let got: async.TaskStatus[string][] = sim.run_tasks(d, entries, [None, None]);
// /b answers at 15 ms of virtual time and /a at 35 ms
```

Faults are scripted answers:

- `sim_fetch.reset()` closes the connection with nothing sent.
- `silent()` never answers: the client's timeout or a cancellation ends
  the fetch.
- `cut(n)` sends only the first `n` bytes.
- `held()` keeps the connection open and quiet after those bytes.
`n.flaky(addr, port, path, percent, fault)` answers that share of the
requests with `fault`, chosen by the sim's seeded generator.
`sim.sweep_seeds(n, prop)` runs `prop(seed)` over seeds `1..n` and
answers the first seed that fails (0 if all pass), and `Sim.rng_state()`
is there for lockstep assertions. See `tests/stdlib/sim_fetch_test.fern`
and `tests/stdlib/sim_platform_test.fern`.

## 10. The reactor: `async.Driver`

`async.Driver` is the reactor that a scheduler watches descriptors
with: `watch`, `unwatch`, `wait_into`, signals, the parent watch, and the
io_uring receive and zero-copy send paths. `async.real_driver()` is
epoll, kqueue or wasi:io/poll, and `real_driver_io_uring()` uses
io_uring on Linux. `std/serve`'s loop is generic over it, and `sim.Sim`
implements it for tests of the loop. Futures and tasks do not use it
directly.

## Current limitations

- A task parks only in compiled code, on x86-64, arm64 and wasm alike.
  The interpreter runs a spawned task to its end in place (section 7).
- A task cancelled while the fetch client resolves a name sees the
  resolver's `NoReply` (as `FetchError.Dns`), not `Cancelled`: the DNS
  exchange takes a cancelled wait as a passed deadline. Its connect and
  read waits report the cancellation (`net.Interrupted`,
  `net.Abandoned`), which the client maps to `FetchError.Cancelled`.
- On wasm a wait's pairs are made into wasi:io pollables for each wait,
  and a deadline is a `monotonic-clock` timer pollable in the same poll.
