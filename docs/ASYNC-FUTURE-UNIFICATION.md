# PR5 — unifying the futures (design)

> Status: **design / not yet implemented.** The fifth and final slice of
> the async redesign (`docs/ASYNC-REDESIGN.md`). PR1–PR4 are merged: the
> colorless combinator surface (`std/async`: `Future[T]` + `gather` /
> `race` / `with_deadline` + `fetch_future`) is live and does real
> overlapping I/O on native. This doc decides *how* to finish the job —
> make `Future[T]` resolve on wasm via the host scheduler, and collapse
> the leftover runtime modules — and deliberately picks the **lighter**
> of two architectures.

## What's still split

After PR4 the surface is unified (`Future[T]` everywhere), but the
*plumbing* underneath is not:

1. **`Pending` futures don't resolve on wasm.** `std/async`'s
   `Pending(fd, resume)` is driven by the `poll` builtin, which is real
   on native (poll(2)/ppoll(2)) but a `-1` **stub** on wasm/interp. So
   `gather`/`race` over real I/O only overlap on native today.
2. **Two async mechanisms on wasm.** Real wasm async currently goes
   through the *separate* `@import(...) async function` path
   (`ExternFunc.Async`, `canon lower async`, `stream[T]` — see
   `docs/WASI-PREVIEW3-ASYNC-PLAN.md`), which is IR-level and
   host-scheduled but is a **different surface** than `Future[T]`.
3. **Three reactor modules linger.** `std/task` (in-memory + native-fd),
   `std/reactor` (native-fd `IoStep[T]`), `std/wasm_reactor`
   (pollable-based) — the pieces `std/async` was distilled from. They
   still exist and have a few direct callers (e.g.
   `reactor_socket_test.go`, `async_runtime_test.fern`).

PR5 closes all three.

## Two architectures considered

### Option A — a first-class IR `future<T>` type (heavy)

Promote `Future[T]` to a type the compiler understands, lowered per
backend: a struct/handle on native, a component-model `future`/subtask
on wasm. `gather`/`race` become IR operations.

- **Cost:** new IR type + type-system integration + codegen on x86-64,
  arm64, **and** wasm; the combinators move from library code into the
  compiler. Large, touches all three backends, high regression surface.
- **Benefit over B:** none that the use case needs — `Future[T]` as a
  plain monomorphized enum already compiles and runs on every backend.

### Option B — keep `Future[T]` a library type; make the *wait primitive* universal-real (light) — RECOMMENDED

The realisation: **`poll` is already the universal readiness
abstraction.** `Future[T]` is a pure library enum
(`Ready(v) | Pending(token, resume)`) that the native compiler already
monomorphizes onto every backend — it needs **no** IR knowledge. The
only thing that isn't portable is the *wait*: `poll(tokens, timeout)` is
real on native and a stub on wasm.

So PR5 is mostly: **make `poll` real on wasm**, by lowering it to the
existing `wasm_poll(list<pollable>)` reactor primitive
(`internal/codegen/wasmbin`, `wasi:io/poll`). The i32 "token" a
`Pending` future carries is an **fd on native** and a **pollable handle
on wasm** — the same `i32`-handle indirection `poll` already uses. Then
`Pending` futures resolve on wasm through the host's `poll`, and
`gather`/`race`/`with_deadline` work over real wasm I/O **unchanged**.

- **Cost:** one backend touched (wasm: lower `poll(i32[], i32)` to
  `wasm_poll` over pollable handles), plus stdlib wasm future
  constructors. No new IR type, no type-system change, combinators stay
  as library code.
- **Risk:** contained to the wasm `poll` lowering (pollable handle ↔
  i32, `list<pollable>` marshalling) — the same shape `std/wasm_reactor`
  already exercises, so the primitive is proven; PR5 just routes the
  generic `poll` builtin to it.

**Decision: Option B.** It reuses what's built (poll-as-universal-
abstraction, the proven wasm pollable reactor) and keeps the surface as
ordinary library code, consistent with the whole redesign's thesis
(concurrency is library combinators, not compiler machinery).

> Note on `@import async function`: that path stays for *direct*
> host-async imports and `stream[T]`. Where it overlaps `Future[T]`
> (a host call you want to `gather`), the bridge is a stdlib
> constructor that returns `Pending(pollable, resume)` wrapping the
> import's pollable — not a compiler change. Fully folding
> `canon lower async` results into `Future[T]` is a possible later
> refinement, explicitly out of PR5 scope.

## Plan (sub-slices)

1. **PR5a — wasm `poll` over pollables.** Lower the generic
   `poll(tokens: i32[], timeout_ms): i32` builtin on wasm to
   `wasm_poll(list<pollable>)` (treating the i32 tokens as pollable
   handles), replacing the `-1` stub. Deterministic test: two
   `wasm_timer_pollable` handles driven through `poll` (the shorter
   ready first), mirroring the native `TestReactorTimers`.
2. **PR5b — wasm future constructors.** A wasm `fetch_future` (over
   `wasi:http` / `tcp_pollable`) returning `Pending(pollable, resume)`,
   so `async.gather([...])` does a real overlapping wasm fetch. e2e:
   the `TestAsyncFetchFutureFanout` shape, on wasm.
3. **PR5c — consolidate the reactors.** Port the direct callers of
   `std/reactor` (`reactor_socket_test.go`) and the hand-rolled
   `std/task` example (`async_runtime_test.fern`) onto `std/async`,
   then **delete** `std/reactor` and `std/task` (and `std/wasm_reactor`
   once PR5a/b absorb it). One `Future[T]` abstraction remains.
4. **PR5d — docs.** Fold this doc's outcome into `ASYNC.md` (drop the
   "Pending resolves only on native" + "three modules linger"
   limitations) and retire `ASYNC-IMPLEMENTATION-PLAN.md` /
   `WASM-REACTOR-PLAN.md` to historical.

Each slice: branch → commit → push → PR → subscribe → merge on green.
PR5a is the only one touching codegen; b–d are stdlib + tests + docs.

## Open questions / risks

- **Pollable handle lifetime.** wasi pollables are owned resources; a
  `race` loser's pollable must be dropped (`wasm_pollable_drop`) so the
  host stops the I/O. Native already leaves fds to process exit; wasm
  needs explicit drop on the abandoned `Pending`. PR5b must thread drop
  into `race`/`with_deadline`'s loser path (the native side can adopt
  the same `tcp_close`-on-loser for symmetry).
- **`list<pollable>` marshalling.** `poll`'s `i32[]` → `list<pollable>`
  is the one non-trivial lowering; `wasm_poll` already takes that shape,
  so PR5a is wiring, not new ABI.
- **interp.** Stays a `-1` stub (no real fds/pollables); `Ready` futures
  cover interp tests. Unchanged.
- **Scope discipline.** Folding `canon lower async` *results* into
  `Future[T]` (so an `@import async` call yields a `Future`) is
  tempting but is the Option-A slope — explicitly deferred. PR5 makes
  the *combinators* work on wasm; it does not rebuild the import path.
