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

**UPDATE — the multiplexer lands too.** `wasm_poll(pollables: i32[])`
now wraps `wasi:io/poll.poll(list<pollable>) -> list<u32>` (the wasm
analog of the native `poll(fds)`): it blocks until the first pollable
in the array is ready and returns its index (or -1). Verified two
timers (200ms + 10ms) → `wasm_poll` returns the short one's index and
short-circuits in ~20ms (does not wait 200ms).

- A Fern `i32[]` is length-prefixed (count at `ptr-4`, contiguous
  elements at `ptr+0`), so the pollable list lowers directly to the
  canonical `(ptr, len)` list param; the `list<u32>` of ready indices
  comes back via an 8-byte return area (data ptr @ +0, count @ +4).
- The shared `WasiIoPollInstanceTypeBody` is parameterized
  (`withPoll bool`): sockets keep the byte-identical block-only shape
  (pinned by a bytes test); `req.Poll` opts into the heavier instance
  that also declares `poll`. `ComposeRequest.Poll` (classified from the
  `poll` import) drives `g.needPoll`, and the `poll` lowering is a
  `gMemRealloc` import (list-out needs `cabi_realloc`; the CLI now
  rebuilds with `ForceMemorySection` when `req.Poll`).
- Builtin `wasm_poll` wired through wasmbin (import spec
  `wasi_io_poll_poll`, helper `__fern_wasm_poll`) + checker FuncSig.
  Tests: `TestWasmReactorPollFirstReady` (e2e), component
  validate/bytes, checker sig.

**UPDATE — the scheduler lands too (`std/wasm_reactor`).** The
pollable scheduler is now a pure-Fern module,
`internal/stdlib/std/wasm_reactor.fern` — the wasm twin of
`std/reactor`: a generic `Step[T]` (`Done(T)` / `Wait(pollable,
resume)`) and `run[T](states, not_ready)` that gathers the waiting
tasks' pollables, blocks in `wasm_poll`, resumes the first-ready task,
and repeats — returning results in **task order**. Where `std/reactor`
multiplexes raw fds via the native `poll`, this multiplexes pollables
via `wasm_poll`; no native-only builtin, so concurrent fan-out works on
the wasm edge target. (`sleep_pollable` is sugar over
`wasm_timer_pollable` for timer tasks.) wasm-only, the codegen
counterpart of native-only `std/reactor` — import whichever matches the
target.

Verified end-to-end on wasmtime (`internal/e2e/wasm_reactor_test.go`):
two overlapped timer tasks resume and return their values in task order
(not completion order) for both `Step[i32]` and `Step[string]` (the
string case exercises the generic-variant inference through the
function-typed `Wait` payload on wasm). `std/wasm_reactor` also passes
the standalone-typecheck gate.

**UPDATE — pollable resource-drop lands.** `wasm_pollable_drop(p)`
wraps `wasi:io/poll.[resource-drop]pollable`, and `std/wasm_reactor.run`
now drops each pollable right after its task resumes (the handle has
fired and won't be polled again), so a long-running reactor frees fired
timer pollables instead of leaking them until component exit. The
standalone `[resource-drop]pollable` lowering (`ComposeRequest.
PollableDrop`, classified from the drop import) is gated off `Tcp`/`Udp`
— the socket paths already declare their own pollable drop, so there's
no duplicate. Tested: `TestWasmReactorTimerBlockDrop` (timer → block →
drop) + the scheduler tests exercise drop on every resumed task; socket
composition unaffected.

**UPDATE — `run_deadline` lands** (parity with native
`std/reactor.run_io_deadline`): `std/wasm_reactor.run_deadline(states,
deadline_ns, not_ready)` adds a single deadline timer pollable to every
poll round (poll-index 0); when it fires first the loop abandons the
still-waiting tasks, landing `not_ready` in their slots — happy-eyeballs
with an SLA upper bound. Tested both the beat-the-deadline and
timed-out paths (`TestWasmReactorRunDeadline`).

**UPDATE — `select` lands across all three reactor flavors.** First-
wins / race: drive tasks until the FIRST completes, return its value,
cancel the rest. `std/wasm_reactor.select` (drops the losers' pollables
via `__wr_drop_waiting`), native `std/reactor.run_io_select` (leaves
fds to the caller — the reactor never owns fd lifecycle), and the
portable `std/task.select` (pre-existing). The scheduler API is now at
parity: **`run` (all) · `run_deadline`/`run_io_deadline` (SLA) ·
`select`/`run_io_select` (first-wins)** on every backend.

## Remaining: wasm outbound TCP client (the next async slice)

The reactor core is complete; the one large remaining piece is wiring
real connection pollables into `run`/`select` for **overlapped outbound
fetch** — the wasm analog of native `TestReactorFanoutBodies`. Today the
wasm TCP path is **server-only** (bind/listen/accept in `wasi_tcp.go` +
the composer's `tcp-socket` methods). Outbound needs the connect chain.
Feasibility confirmed: `wasmtime run -S inherit-network` already drives
the wasm TCP **server** e2e tests, so an outbound client is testable
against a Go upstream the same way.

The ABI groundwork (the historically fiddly part) is done — verified by
`wasm-tools print` of a compiled server component. The
`wasi:sockets/tcp@0.2.0` instance type (`WasiSocketsTcpInstanceTypeBody`,
component.go) numbers types **0–21** (exports do NOT consume type
indices; the six method functypes are consecutive 16–21). Extend it
with `withConnect` (parameterize like `WasiIoPollInstanceTypeBody`'s
`withPoll`, so the server path stays byte-identical):

- type 22: `tuple<own input-stream=11, own output-stream=12>` — `InnerTypeTuple([]byte{0x0b,0x0c})`
- type 23: `result<22, error-code=1>` — `InnerTypeResultOkErr(22, 1)`
- type 24: `start-connect(self 7, network 8, remote-address 2) -> result 9` — same shape as start-bind (`tcpMethodFuncDecl(..., []byte{0x07,0x08,0x02}, 0x09)`), export `[method]tcp-socket.start-connect`
- type 25: `finish-connect(self 7) -> result 23` (`tcpMethodFuncDecl("finish-connect", []string{"self"}, []byte{0x07}, 0x17)`), export `[method]tcp-socket.finish-connect`
- decl count 28 (0x1c) → 34 (0x22): +4 types, +2 exports.

Then the rest of the vertical:

1. **wasmbin** (`wasi_tcp.go`): import specs `wasi_sockets_tcp_start_connect`
   / `_finish_connect`; `buildTcpConnectBody` mirroring
   `buildTcpListenBody` + `buildTcpAcceptBody`: create-tcp-socket →
   start-connect(remote ipv4 addr, like start-bind's 15-param
   flattening) → subscribe → `pollable.block` → finish-connect →
   12-byte connection struct `(sock, input-stream, output-stream)`.
   `tcp_recv` / `tcp_send` reuse the existing stream paths.
2. **Fern API + checker**: `tcp_connect(host_be: i32, port: i32): i32`
   (parallel to the native builtin; returns the connection struct ptr
   or `-errno`).
3. **composer** (`compose_unified.go` / `compose_general.go` /
   `classify.go`): `ComposeRequest.TcpConnect` + `g.needConnect`
   threaded into `ensureTcp` (like `needPoll`); classify the
   `start-connect` import; add the connect lowerings (create-socket,
   start/finish-connect, subscribe, pollable.block, the io/streams
   read+write, the drops) for a connect program — the connect
   counterpart of the existing `req.Tcp` server block.
4. **reactor integration + test**: a connection's `subscribe` → pollable
   feeds `std/wasm_reactor.run`/`select`; e2e: a Go upstream +
   `wasmtime -S inherit-network`, fan out 2 connects, recv both bodies
   (the wasm `TestReactorFanoutBodies`).

This is a single large vertical (testable only end-to-end, so it ships
as one PR), best taken as a focused effort — the ABI design above means
it starts from a solved position.

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
