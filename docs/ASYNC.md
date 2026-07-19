# Async & concurrency in Fern (user guide)

Fern's concurrency is **colorless**: there is no `async` function modifier and no
function coloring in the surface you write. Concurrency is expressed with plain
library combinators — `gather` / `race` / `with_deadline` — over a `Future[T]`
(a not-yet-ready value). You write straight-line code and make the concurrency
points **explicit** at the `gather`/`race` call. No stackful green threads, no
parse-time CPS transform — just functions and values.

This is the user-facing guide. For the design and internals see
`docs/ASYNC-REDESIGN.md` (the model + rationale), `docs/STREAM-TYPE-SURFACE.md`,
and `docs/WASI-PREVIEW3-ASYNC-PLAN.md`.

> Status: the combinator surface (`std/async`) drives REAL overlapping socket
> I/O on the native backends (x86-64 / arm64) via the OS reactor; the portable
> `Ready`-future path runs on every backend (interp / wasm / native). The WASI
> Preview-3 `stream[T]` import surface below does real wire I/O on wasm. The next
> milestone is promoting `Future[T]` to a first-class IR type so wasm futures use
> the component-model-async host scheduler (`docs/ASYNC-REDESIGN.md` PR5).

The patterns shown here are exercised by `examples/tests/async_combinators_test.fern`
and the Go e2e suite (`async_combinators_test.go`, `async_fetch_future_test.go`).

---

## 1. Streams: `stream[T]`

An async host import can return a `stream[T]` — a sequence delivered incrementally
over the wire. Under the colorless model the call site sees a plain value:

```fern
@import("wasi:http/types", "body-stream")
async function body(): stream[u8];
```

**Collect it eagerly** — the whole stream into a `u8[]`:

```fern
async function handle(): i32 {
    var bytes: u8[] = body();   // drains the stream to EOF, returns the array
    return bytes.len();
}
```

**Iterate it lazily** — one element at a time, off the wire, before EOF (O(1)
memory, no full buffer):

```fern
async function sum(): i32 {
    var total = 0;
    for x in body() {           // pulls one element per turn
        total = total + (x as i32);
    }
    return total;
}
```

Lazy `for x in stream` works for any scalar element type (`u8` / `i32` / `i64` /
`f64`, …). A `stream[T]` **parameter** is the mirror: you pass an eager `T[]` and
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

You rarely construct a `Future` by hand — the I/O primitives return them
(`fetch.fetch_future`, below). `Ready(v)` is occasionally useful for a value
that's already available (and it resolves on every backend, including interp /
wasm). A `Pending` future resolves when its file descriptor becomes readable,
driven by the OS `poll` on native.

`Future[T]` is **generic over `T`** — `Future[string]` for response bodies,
`Future[i32]` for status codes, etc.

---

## 3. `gather` — fan out and await all

`gather` issues every future's I/O concurrently and returns all results in input
order. This is the canonical edge fan-out: hit a cache and a primary, take both.

```fern
import "std/async";
import "std/fetch";

function handle(): i32 {
    var cache: i32   = fetch.ipv4(10, 0, 0, 1);
    var primary: i32 = fetch.ipv4(10, 0, 0, 2);
    var bodies: string[] = async.gather([
        fetch.fetch_future(cache,   80, "/key"),
        fetch.fetch_future(primary, 80, "/key"),
    ], "");                       // "" is the fallback for a future that can't complete
    return bodies.len();          // both fetches overlapped on one thread
}
```

`gather(fs, on_incomplete)` — the `on_incomplete` value fills any slot whose
future never resolves (a `poll` error, or the `-1` `poll` stub on interp/wasm).
With a blocking native `poll`, live futures always make progress.

---

## 4. `race` — first to finish wins

`race` runs the futures until the **first** resolves, returns `(winnerIndex,
value)`, and abandons the rest (happy-eyeballs / first-wins). Cancellation is
structural: a loser is simply never resumed.

```fern
import "std/async";
import "std/fetch";

function fastest(a: i32, b: i32): string {
    var fs: async.Future[string][] = [
        fetch.fetch_future(a, 80, "/k"),
        fetch.fetch_future(b, 80, "/k"),
    ];
    var (winner, body) = async.race(fs, "");
    // `winner` is the index that finished first; `body` its result.
    return body;
}
```

(It's spelled `race`; the older `race { … }` keyword block was removed in favour
of this combinator.)

---

## 5. `with_deadline` — await all within a budget

`with_deadline(ms, fs, on_timeout)` is `gather` with an SLA: fan out, take
whatever answers within `ms` wall-clock milliseconds, and drop the stragglers
(their slots get `on_timeout`).

```fern
var bodies: string[] = async.with_deadline(250, [
    fetch.fetch_future(cache,   80, "/k"),
    fetch.fetch_future(primary, 80, "/k"),
], "");   // any upstream slower than 250ms lands as ""
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

var d: sim.Sim = sim.new(7);
var fs: async.Future[string][] = [
    sim.future_at(d, 40000000, "late"),   // ready at 40ms of virtual time
    sim.future_at(d, 10000000, "early")   // ready at 10ms
];
var got: Option[string][] = async.with_deadline_on(d, 25, fs);
// got == [None, Some("early")], and d.now_ns() == exactly 25000000
```

`sim.future_at(d, at_ns, v)` resolves to `v` at virtual time `at_ns`;
`sim.future_chain(d, at_ns, step_ns, n, v)` re-suspends `n` times (the
`__fetch_drain` shape) before resolving. See
`examples/tests/sim_driver_test.fern`.

### SimNet — scripted upstreams

`sim.Net` is the sim sibling of `fetch.fetch_future`: register scripted
endpoints (host/port, optional path — `""` is a host:port wildcard — body,
first-byte latency, chunking schedule), then fetch them through the
combinators. The futures honour the real fetch contract: the body on
success, `""` immediately for an unregistered (dead) upstream, one
re-suspension per scheduled chunk with the accumulated body resolving at
the last chunk's virtual time. Registration is value-returning
(`n = n.serve(...)`); each endpoint carries a shared `hits` counter for
call assertions.

```fern
var d: sim.Sim = sim.new(1);
var n: sim.Net = sim.net(d);
n = n.serve(1, 80, "/k", "primary", 30000000);          // one chunk at 30ms
n = n.serve_chunked(2, 80, "/big", "abcdefghij",
        5000000, 5000000, sim.chunks_of(10, 4));        // [4,4,2] at 5/10/15ms
var fs: async.Future[string][] = [
    n.fetch_future(1, 80, "/k"),
    n.fetch_future(2, 80, "/big"),
    n.fetch_future(9, 80, "/k")                          // unregistered -> Ready("")
];
var got: string[] = async.gather_on(d, fs, "");
// got == ["primary", "abcdefghij", ""], d.now_ns() == exactly 30000000,
// n.hits(1, 80, "/k") == 1
```

See `examples/tests/sim_net_test.fern`.

### Fault injection — seed-driven flaky upstreams

Registered endpoints take injected faults, value-returning like `serve`
(a fault registration for an unknown endpoint is a no-op; faulted
fetches still count as hits):

- `n.fault_fail(host, port, path)` — every fetch resolves immediately to
  `""`, the real fetch's connect/send failure.
- `n.fault_stall(host, port, path)` — every fetch suspends on a
  never-ready token and never resolves: `gather_on` fills the slot with
  `on_incomplete`, `with_deadline_on` drops it with `None` at exactly
  the virtual deadline.
- `n.fault_partial(host, port, path, k)` — the half-dead connection:
  the first `min(k, #chunks)` scheduled chunks arrive at their normal
  virtual times, then the upstream goes silent and the future never
  resolves (even `k >= #chunks` withholds the terminating close;
  `k <= 0` stalls before the first byte).
- `n.fault_flaky(host, port, path, p_percent)` — probabilistic wrapper
  over the endpoint's fault mode (defaulting it to fail): every fetch
  of a flaky endpoint consumes exactly one draw from the sim PRNG, in
  program order, and the fault fires when the draw lands below
  `p_percent`.

Because the flaky draws come from the same seeded PRNG as `poll_ready`'s
tie-breaks, a whole flaky fan-out is a pure function of `sim.new(seed)`
and the call order — a failure is a seed you replay, not a flake.
`sim.sweep_seeds(n, prop)` is that workflow in miniature: run
`prop(seed)` over seeds `1..n` and return the first failing seed (0 if
all pass), with `Sim.rng_state()` available for lockstep assertions.
See `examples/tests/sim_fault_test.fern`.

That purity claim is itself property-tested: the harness in
`internal/e2e/sim_property_test.go` generates random sim programs —
scripted endpoints with random latencies, chunk schedules, and fault
modes, driven through random `gather_on` / `race_on` /
`with_deadline_on` pipelines — and requires interp, native x86-64,
and wasm to print byte-identical digests (results, winners, `None`
slots, `now_ns`, `rng_state`, hits). Any backend divergence on a sim
program is a real bug, never a flake; a failure prints the whole
program and its generator seed for replay
(`TestSimProperty` / `TestSimProperty_Regressions` /
`FuzzSimProperty`).

## 7. The awaitable fetch: `fetch.fetch_future`

```fern
pub function fetch_future(host_be: i32, port: i32, path: string): async.Future[string]
```

Opens the connection and sends the request **non-blocking**, then returns a
`Pending` future that resolves to the response **body** once the socket is
readable. Drive several through `gather` / `race` / `with_deadline` and their
reads overlap on one thread. A connect/send failure resolves immediately to `""`
so a dead upstream never stalls the fan-out. (`host_be` is the IPv4 address in
network byte order — build it with `fetch.ipv4(a,b,c,d)`.)

---

## 8. How it works (one paragraph)

A `Future[T]` is either a ready value or an fd plus a continuation. The
combinators gather the pending futures' fds, block once in the universal `poll`
builtin (poll(2) / ppoll(2) on native; a `-1` stub on interp/wasm), resume the
future whose fd is ready (its continuation does the actual non-blocking read),
and repeat until done. One thread, overlapping I/O, no function coloring, no
green-thread stacks, no compiler transform — it rides enums, closures, and loops
that every backend already lowers. See `docs/ASYNC-REDESIGN.md`.

## Current limitations

- `Pending` futures resolve on **native** (fd-backed, via poll(2)/ppoll(2)) and
  on **wasm** (pollable-backed, via wasi:io/poll — `poll` forwards to it). The
  wait token is portable: `fetch_future` uses `tcp_pollable(c)`, which is the raw
  fd on native and a real wasi:io/poll pollable handle on wasm. So a real
  overlapping `gather([fetch_future, …])` works on **both native and wasm**. On
  **interp** the `poll` stub means `Pending` never completes (the portable
  `Ready`-future path works everywhere).
- `with_deadline` enforces its deadline on **both** native and wasm: native via
  `poll(2)`'s timeout arg; wasm by appending a real timer pollable
  (`monotonic-clock` `subscribe-duration`) to the poll set each round, so the
  timer firing is the deadline. (This needed the composer to export both `now`
  and `subscribe-duration` on one `monotonic-clock` import instance — see
  `docs/ASYNC-FUTURE-UNIFICATION.md`.)
- `race` / `gather` drop every abandoned future's pollable before teardown
  (`__drop_losers` → `wasm_pollable_drop`), so a `race` over real wasm sockets
  no longer leaks the losers' pollables (which are children of their sockets and
  would otherwise trap wasmtime with "resource has children").
- `fetch_future` reads the **whole** response: its continuation re-suspends per
  chunk (`__fetch_drain`), accumulating across reads until EOF, so a body larger
  than one `recv` buffer / spread over TCP segments comes back in full while
  staying overlapped with the other futures in a `gather`/`race`.
- `std/async` is now the **single** reactor — the legacy `std/task`, `std/reactor`,
  and `std/wasm_reactor` modules it was distilled from have all been deleted.
  `gather` / `race` / `with_deadline` over `Future[T]` cover the native (fd) and
  wasm (pollable) paths alike.
