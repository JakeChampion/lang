# Wasm reactor — design + the component-composition blocker

The native reactor (poll/ppoll → `std/reactor.run_io` → timer_fd →
timeouts → real sockets → outbound fan-out) is complete on x86-64 +
arm64. This doc scopes the **wasm** reactor — the primary edge target
(WASI) — and records the real blocker found while implementing it, so
the dedicated effort starts from a known position.
(Companion to docs/ASYNC-IMPLEMENTATION-PLAN.md Phase 1c.)

## Why wasm needs a different shape than native

Native multiplexes raw fds via `poll(2)`/`ppoll(2)`. WASI Preview 2 has
no fds — readiness is a **`pollable` resource**, and the multiplexer is
`wasi:io/poll.poll(list<pollable>) -> list<u32>`. Timers come from
`wasi:clocks/monotonic-clock.subscribe-duration(ns) -> own<pollable>`.
So the wasm reactor is pollable-based, not fd-based; it can reuse the
`IoStep`/scheduler *shape* but over pollable handles + `poll` instead
of fds + the native `poll` builtin.

## Layer 1 — wasmbin import/builtin layer: STRAIGHTFORWARD (verified)

Implemented locally and verified to typecheck + emit a core module
(then reverted pending Layer 2). The steps, all small and mirroring
`__fern_monotonic_ns` / the tcp helpers:

- `internal/codegen/wasmbin/wasi.go`: add import specs. Crucially,
  `subscribe-duration` returns its `own<pollable>` **directly** as an
  i32 (like `tcp-socket.subscribe`, `results: [i32]`) — no return
  area:
  ```go
  "wasi_clocks_subscribe_duration": {
      module: "wasi:clocks/monotonic-clock@0.2.0",
      name:   "subscribe-duration",
      params: []byte{encode.ValtypeI64}, results: []byte{encode.ValtypeI32},
  }
  ```
  (`wasi_io_pollable_block` already exists.) `poll(list<pollable>) ->
  list<u32>` is `params: [i32 ptr, i32 len, i32 retptr], results: nil`
  (list result via return area — the backend already marshals lists:
  see `wasi_tcp.go` blocking-read and `wasi_http.go` fields.entries).
- `runtime.go`: `runtimeHelperSpec`s for `__fern_wasm_timer_pollable`
  (i64→i32), `__fern_wasm_block` (i32→i32), + `wasm_poll` later; a
  `scanRuntimeHelpers` case each.
- Body builders (mirror `buildMonotonicNsBodyP2`): timer = LocalGet ns
  → Call subscribe-duration; block = LocalGet p → Call pollable.block →
  i32.const 0.
- `wasmbin.go` `CallDirectAliases`: `wasm_timer_pollable` /
  `wasm_block` / `wasm_poll` → their `__fern_*`.
- `wasi.go` `scanImports`: add the imports when the helpers are used.
- `internal/checker/checker.go`: `FuncSigs` for the builtins.

## STATUS UPDATE — timer pollable lands end-to-end (composer blocker solved)

The composer blocker described below is **solved for the timer path**.
A wasm program can now create a `wasi:io/poll` pollable from a timer
and block on it until ready, composed **standalone** (clocks + io/poll,
no socket), running on the stock **wasmtime 34** the rest of the suite
uses — pollables are Preview 2, so no Preview 3 / wasmtime upgrade was
needed.

- Builtins (Layer 1): `wasm_timer_pollable(duration_ns: i64): i32`
  (→ `monotonic-clock.subscribe-duration`, returns the pollable) and
  `wasm_block(pollable: i32): i32` (→ `pollable.block`). Wired in
  `internal/codegen/wasmbin/` (import spec `wasi_clocks_subscribe_duration`,
  helpers `__fern_wasm_timer_pollable` / `__fern_wasm_block`, scanImports,
  CallDirectAliases) + `internal/checker` FuncSigs.
- Composer (Layer 2): `ComposeRequest.Timer`, `ensureMonotonicTimer`
  (pulls in `ensureIoPoll`, outer-aliases the surfaced `pollable` into
  the clock instance so `subscribe-duration`'s `own<pollable>` is the
  SAME resource `pollable.block` consumes), and
  `WasiClocksMonotonicTimerInstanceTypeBody`. `classify.go` maps the
  `subscribe-duration` import → `req.Timer`.
- Tests: `internal/e2e/wasm_reactor_test.go` (compile → component →
  wasmtime, timer block + timer-with-stdout), component bytes/validate,
  checker sig test. Verified a 500ms timer actually blocks ~500ms.

**Remaining for a true multiplexing reactor:** the list multiplexer
`wasi:io/poll.poll(list<pollable>) -> list<u32>` (the wasm analog of
the native `poll(fds)`), so N timers/socket-pollables can be awaited
together and the first-ready resumed. That adds one method to the
io/poll instance type (list param + list result via return area —
mirror `fields.entries`) + a `wasm_poll` builtin, then the
`IoStep[T]`-shaped scheduler over pollables. The hard cross-instance
resource composition is already done; this is the next slice.

## Layer 2 — component composer: THE BLOCKER (historical — now solved for timers)

`fern -target wasm` wraps the emitted core module into a Preview-2
**component** via `internal/wasm/component`. `ClassifyCore`
(`classify.go`) routes each core WASI import to a real implementation;
anything unrecognised → `unsupported` → the compile fails with
"can't wrap a core module with unrecognised imports".

Two problems for the pollable timer:

1. **`subscribe-duration` is unsupported.** It hits the `default` arm,
   isn't in `knownPreview2Imports`, → `unsupported`. It can't just be
   added to `knownPreview2Imports` the way `monotonic-clock.now` is:
   `now` is a **scalar** capability (`resultValtypes: [u64]`), whereas
   `subscribe-duration` returns an `own<pollable>` **resource** and
   ties into the `pollable` resource type + its `block`/`drop` methods.

2. **`wasi:io/poll` is only "accepted implicitly"** (`classify.go`
   ~104) — composed solely as a side effect of the TCP/UDP shapes
   (`req.Tcp`/`req.Udp`), which pull in real `wasi:sockets` components
   that bring the `pollable` resource. There is no standalone
   composition of `wasi:io/poll` + the `pollable` resource for a
   non-socket program.

So the wasm reactor needs **resource-aware composition**: compose the
real `wasi:clocks/monotonic-clock` (now + subscribe-duration +
pollable) and standalone `wasi:io/poll` (poll + pollable block/drop)
components, trafficking the `pollable` resource through the component
boundary — the same machinery the socket shapes use, generalised to a
clocks-driven, socket-free program. That's the substantial, specialized
piece; Layer 1 is trivial on top of it.

## Recommended approach for the dedicated effort

1. Determine how the composer satisfies the socket shapes' `pollable`
   resource today (real-component link vs synthesised shim) in
   `compose_unified.go` / `compose_general.go`.
2. Add a `req.Clocks`/`req.Poll` (or a `Structured` resource-capable)
   path that composes real `wasi:clocks/monotonic-clock` +
   `wasi:io/poll` with their `pollable` resource, independent of
   sockets.
3. Then Layer 1 (the wasmbin builtins above) + a deterministic
   wasmtime test: two `wasm_timer_pollable`s, the shorter ready first
   via `wasm_poll` — the wasm analog of `TestReactorTimers`.

wasmtime (v34.0.1, /root/.fern-wasm/wasmtime) + wasm-tools are
available, so it's fully testable once Layer 2 lands.

## Status of the broader async work

Native async/edge-handler feature: COMPLETE + merged (serve,
`plat.fetch`/`get_url`, overlapped fan-out returning bodies, timers,
timeouts, select/cancellation; x86-64 + arm64). Blockers handed off:
self-host fn-payload-variant (docs/SELF-HOST-FN-PAYLOAD-VARIANT-GAP.md,
#3552), generic-variant fn-payload inference
(docs/GENERIC-VARIANT-FN-PAYLOAD-INFERENCE-GAP.md), and this wasm
component-composition layer.
