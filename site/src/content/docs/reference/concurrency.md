---
title: Concurrency
description: Colorless futures over one poll loop, deterministic simulation testing, and why Fern has no threads.
sidebar:
  order: 7
---

Fern's concurrency is I/O overlap, not parallelism. There is one thread,
one poll loop, and a `Future[T]` you compose with ordinary library
functions. There are no threads, and there will not be until the memory
model changes — reference counts are non-atomic by design, and a second
context racing them is unsound.

That is a real limit, and it is the honest headline. Within it, the model
is unusually simple.

## No function colouring

`Future[T]` is a plain enum in `std/async`:

```fern
pub enum Future[T] {
  Ready(T),
  Pending(i32, (i32) => Future[T]),
}
```

`Ready` is a value you already have. `Pending` is a token to wait on plus
the continuation to run when it is ready. Nothing about a function's
signature changes because it produces one, so there is no `async` colour
creeping up the call graph and no `await` sprinkled through the body. The
concurrency point is where you call a combinator, and nowhere else.

## Combinators

```fern
import "std/async";
import "std/fetch";
import "std/i32";

function main(): i32 {
  var fs: async.Future[u8[]][] = [
    fetch.fetch_future(fetch.ipv4(93, 184, 216, 34), 80, "/a"),
    fetch.fetch_future(fetch.ipv4(93, 184, 216, 34), 80, "/b"),
  ];

  // Both requests are in flight at once; this returns when both land.
  var empty: u8[] = [];
  var bodies: u8[][] = async.gather(fs, empty);
  print(bodies[0].len().to_string());
  return 0;
}
```

- **`gather(futures, on_incomplete)`** runs them all and returns every
  value, substituting `on_incomplete` for anything that never resolved.
- **`race(futures, none_val)`** returns `(index, value)` for the first one
  home.
- **`with_deadline(ms, futures)`** returns `Option[T][]` — `None` for the
  ones that missed the deadline.

Each has an `_on` sibling (`gather_on`, `race_on`, `with_deadline_on`) that
takes an explicit driver. That parameter is what makes the next section
possible.

## Deterministic simulation

The `Driver` trait is the seam where everything nondeterministic happens —
waiting, reading the clock, setting a timer, dropping a token:

```fern
pub trait Driver {
  function poll_ready(self: Self, toks: i32[], timeout_ms: i32): i32;
  function now_ns(self: Self): i64;
  function timer(self: Self, ns: i64): i32;
  function drop_token(self: Self, tok: i32): i32;
}
```

`std/sim` implements it over a **virtual clock** that starts at zero and
only advances inside `poll_ready`, with a seeded PRNG breaking every tie.
Run a fan-out through `gather_on(sim, …)` and the whole schedule becomes a
pure function of the seed — identical on native, on WebAssembly, and in the
interpreter. A concurrency bug stops being a flake and becomes a seed you
replay.

`sim.Net` extends that to a simulated network, with fault injection built
in: `fault_fail`, `fault_stall`, `fault_partial` and
`fault_flaky(p_percent)`. `sim.sweep_seeds(n, prop)` runs a property across
`n` seeds and returns the first one that fails, or `0`.

## Where futures actually resolve

`Ready` futures resolve on every backend, so a fan-out of already-computed
values — the shape most tests take — works everywhere including the
interpreter.

`Pending` futures are driven by the real `poll` builtin, which is `poll(2)`
/ `ppoll(2)` on the native targets. On the interpreter and on WebAssembly
that builtin is a stub that returns `-1`, so an fd-backed future never
resolves there. **Real socket I/O is a native-target feature today.** The
WebAssembly path is component-model async, and folding it in is the next
milestone rather than something you can rely on now.

One thing that does work on WebAssembly: `async function` on an export.
It changes nothing about the body — the interpreter runs it as an ordinary
function — and only marks the export for `canon lift async` in the emitted
component. WASI Preview 3's `stream[T]` is available there too, either
collected eagerly or iterated lazily in constant memory:

```fern
@import("wasi:http/types", "body-stream")
async function body(): stream[u8];
```

## Processes and signals

Where futures are not the right shape, the platform surface is:
`proc_fork`, `proc_exec` and `proc_waitpid` for subprocesses, and
`std/signal` for signal dispositions. Note that `std/signal` deliberately
offers no handler-installing form — a handler would be a second context
racing the same non-atomic refcounts that rule out threads.

## The retired keyword surface

Older material may mention `concurrent { … }`, `await` and `race { … }` as
syntax. That surface, and the parser-side CPS transform behind it, was
removed. The library combinators above replace it entirely.
