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

## 6. The awaitable fetch: `fetch.fetch_future`

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

## 7. How it works (one paragraph)

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
- `with_deadline`'s deadline is **native-only** for now: `poll` ignores its
  `timeout_ms` arg on wasm, so on wasm `with_deadline` waits for all futures
  like `gather` (a host timeout needs a timer pollable added to the poll set).
- On **wasm**, a `race` over real sockets currently leaks the losers' pollables
  (they're never dropped, since only the winner's continuation runs) — fine for a
  short-lived handler (process exit reclaims), but `gather` is the clean path
  (every future resolves, so every pollable is dropped before its socket closes).
- `fetch_future`'s continuation does a single `recv`, sufficient for the small
  responses of the edge fan-out; a multi-chunk body that re-suspends per chunk
  is folded in with the IR future.
- `std/async` is now the single native reactor — the legacy `std/task` and
  `std/reactor` modules it was distilled from have been deleted. `std/wasm_reactor`
  (pollable-based) remains until the wasm slice folds it into `Future[T]`.
