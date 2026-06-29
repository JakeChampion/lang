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

## Revised finding: the wasm slice is deeper than first scoped

Investigation before cutting code (against `internal/codegen/wasmbin` +
`internal/wasm/component`) found the original "PR5a = one low-risk
codegen change" framing too optimistic, in two ways:

1. **It pulls in the component composer, not just a function body.**
   `poll`'s body change (call `__fern_wasm_poll` instead of returning
   `-1`) is trivial. But `__fern_wasm_poll` calls the `wasi:io/poll`
   import, whose **component composition** — the `wasi:io/poll`
   instance, the `pollable` resource's `block` / `[resource-drop]`, and
   `cabi_realloc` — is wired by the composer (`compose_general.go`,
   `compose_unified.go`, `classify.go`), today **only** for programs
   that use `std/wasm_reactor`. Routing the generic `poll` builtin there
   pulls that machinery into **every** `std/async` wasm program. That is
   the highest-risk surface in the codebase, not a localised helper edit.

2. **PR5a is meaningless — and unsafe — without PR5b.** On wasm the
   i32 tokens a `Pending` future carries must be real **pollable
   handles**. A `std/async` wasm program produces none today (there is
   no wasm `fetch_future` yet — that is PR5b). So PR5a *alone* changes
   wasm `poll` from "always `-1`" to "treat arbitrary i32s as pollable
   handles and call the host" — at best `-1`-equivalent, at worst a
   **trap** on an invalid handle. The two must ship together, and the
   composer must **gate** the `wasi:io/poll` wiring on actual pollable
   production (don't wire it for a program that only ever holds `Ready`
   futures).

The revised plan folds 5a+5b into one composer-touching slice and
sequences the safe consolidation first.

## Plan (sub-slices, revised)

1. **PR5c — consolidate the reactors (do this first; no codegen).**
   Port the direct callers of `std/reactor` (`reactor_socket_test.go`)
   and the hand-rolled `std/task` example (`async_runtime_test.fern`)
   onto `std/async`, then **delete** `std/reactor` and `std/task`. Pure
   stdlib + tests; collapses the duplicate native reactors into the one
   `Future[T]` abstraction with zero backend risk. (`std/wasm_reactor`
   stays until the wasm slice absorbs it.)
2. **PR5-wasm — real wasm futures via poll-over-pollables. DONE (core).**
   Lowered the generic `poll(tokens: i32[], timeout_ms): i32` on wasm to
   `__fern_wasm_poll` over the i32 pollable handles. **The composer
   gating turned out to be automatic**, not manual: `classify.go` sets
   `req.Poll` by scanning the compiled module's imports, so emitting the
   `wasi:io/poll.poll` import (via `__fern_wasm_poll`) wires the io/poll
   instance on its own — no `compose_*.go` change. The trap concern is
   moot because `gather`/`race` only call `poll` when a `Pending` exists,
   and a wasm `Pending` carries a real pollable handle. Verified:
   `async.gather` / `async.race` over two `wasm_timer_pollable` futures
   resolve through the host on stock wasmtime (`async_wasm_e2e_test.go`,
   i32 + string). `timeout_ms` is ignored on wasm (so `with_deadline`'s
   deadline is native-only — a host timeout would add a timer pollable to
   the set).
   - **DONE — portable `fetch_future` (real overlapping wasm fetch).**
     The wait token is now `tcp_pollable(c)`, made portable by giving
     the existing `tcp_pollable` builtin a **native/interp identity**
     lowering (the fd is its own readiness token on native) — so one
     `fetch_future` builds `Pending(tcp_pollable(c), resume)` on every
     backend. The wasm pollable is a *child* of the socket resource, so
     `resume` drops it (`wasm_pollable_drop`, given a native/interp
     **no-op** lowering) before `tcp_close`, avoiding the
     "resource has children" trap. Verified:
     `internal/e2e/async_wasm_fetch_e2e_test.go` — two parallel
     `fetch.fetch_future` through `async.gather` over real sockets,
     bodies returned in input order, overlapped, under stock wasmtime
     (`-S inherit-network`, Preview 2).
   - **DONE — drop abandoned pollables.** `race` (and `gather` /
     `with_deadline`) now drop every still-`Pending` future's token via
     `__drop_losers` → `wasm_pollable_drop` (a no-op on native/interp).
     A `race` over real wasm sockets no longer leaks the losers'
     pollables (children of their sockets → would trap with "resource
     has children"). Verified by
     `internal/e2e/async_wasm_fetch_e2e_test.go ▸ TestAsyncWasmRaceFetchDropsLoser`.
   - **DONE — `with_deadline` host-timeout on wasm (incl. the composer
     fix).** `with_deadline` appends a deadline timer to the poll set
     each round: native uses `poll(2)`'s timeout arg (the appended timer
     is `-1`, ignored); wasm uses a real `wasm_timer_pollable`
     (monotonic-clock `subscribe-duration`) whose firing IS the
     deadline. A `ready` index of `nfds` (the timer's slot) means the
     deadline elapsed; timed-out futures' pollables are dropped
     (`__drop_losers`). Portable via `wasm_timer_pollable` getting a
     native/interp identity-`-1` lowering (like `tcp_pollable` /
     `wasm_pollable_drop`). **The composer blocker is fixed:** a new
     `WasiClocksMonotonicTimerAndNowInstanceTypeBody` exports BOTH
     `now` and `subscribe-duration` on one monotonic-clock import
     instance, and `ensureMonotonicTimer(withNow)` uses it when the
     program also imports `now` (the structured-`now` path reuses that
     instance, since `importStructured` is a no-op once it exists).
     Verified: the combined `monotonic_ns` + `wasm_timer_pollable` +
     `poll` program now composes/validates/runs, and
     `internal/e2e/async_wasm_e2e_test.go ▸ TestAsyncWasmWithDeadline`
     (fast future beats the budget, slow one lands `on_timeout`).
   - **DONE — folded `std/wasm_reactor` into `std/async`.** Its
     `run` / `select` / `run_deadline` (over pollable-tagged `Step[T]`)
     are exactly `std/async`'s `gather` / `race` / `with_deadline` over
     `Future[T]` now that those resolve `Pending` pollables on wasm, so
     the module + its now-duplicate e2e tests were deleted (the wasm
     primitive tests — `wasm_timer_pollable` / `wasm_poll` /
     `wasm_block` / `wasm_pollable_drop` — stay; they exercise the
     builtins `std/async` rides on). `std/async` is the single reactor;
     `std/task` / `std/reactor` / `std/wasm_reactor` are all gone.

The unification is complete: one `Future[T]` + `gather`/`race`/
`with_deadline`, real overlapping I/O on native (fds) and wasm
(pollables), no leftover reactor modules.
3. **PR5d — docs.** Fold the outcome into `ASYNC.md` (drop the "Pending
   resolves only on native" + "modules linger" limitations) and retire
   `ASYNC-IMPLEMENTATION-PLAN.md` / `WASM-REACTOR-PLAN.md` to historical.

Each slice: branch → commit → push → PR → subscribe → merge on green.
Only PR5-wasm touches codegen + the composer; 5c and 5d are stdlib /
tests / docs.

## Open questions / risks

- **Pollable handle lifetime.** wasi pollables are owned resources; a
  `race` loser's pollable must be dropped (`wasm_pollable_drop`) so the
  host stops the I/O. Native already leaves fds to process exit; wasm
  needs explicit drop on the abandoned `Pending`. PR5b must thread drop
  into `race`/`with_deadline`'s loser path (the native side can adopt
  the same `tcp_close`-on-loser for symmetry).
- **`list<pollable>` marshalling.** `poll`'s `i32[]` → `list<pollable>`
  is straightforward — `__fern_wasm_poll` already takes the `i32`
  array-data-pointer shape (reads `len` at `arr-4`) and returns the
  first-ready index, so `poll`'s wasm body is just
  `local.get 0; call __fern_wasm_poll` (the `timeout_ms` arg is dropped
  for the first cut — a host timeout needs a timer pollable added to the
  set, a later refinement).
- **Gating the composer wiring (the real risk).** The `wasi:io/poll`
  instance + `pollable` resource glue must be added to the component
  *only* when the program actually produces pollables — keyed off a
  `needs`/classify signal, not off "references `poll`". Otherwise every
  `std/async` program (incl. `Ready`-only ones) drags in the io/poll
  import and risks trapping `__fern_wasm_poll` on non-pollable i32s.
  This gating is the crux of the PR5-wasm slice.
- **interp.** Stays a `-1` stub (no real fds/pollables); `Ready` futures
  cover interp tests. Unchanged.
- **Scope discipline.** Folding `canon lower async` *results* into
  `Future[T]` (so an `@import async` call yields a `Future`) is
  tempting but is the Option-A slope — explicitly deferred. PR5 makes
  the *combinators* work on wasm; it does not rebuild the import path.
