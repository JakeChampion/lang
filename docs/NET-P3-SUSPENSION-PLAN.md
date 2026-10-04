# Networking P3 — inferred suspension (plan)

> Status: **plan, 2026-10-04.** Phase P3 of #9851, tracked in #9857. The
> decision it implements is #9856 (B): functions that reach a platform wait
> are lowered to resumable state machines at the IR level, the surface stays
> colorless, the worker's reactor drives them. This document says what that
> costs, where each piece lives, and in what order it lands. Nothing in it is
> built yet.

## 1. What P3 has to deliver

From #9857 and #9851 §5:

- A worker holds thousands of connections while handlers wait on upstream
  I/O (`plat.http`) or pull a streaming request body. Today the handler call
  in `std/serve` is synchronous (`resp = handler(f.request, plat)`), so one
  waiting handler stalls every connection on its worker.
- A client disconnect cancels the handler's subtree; `with_deadline` and
  `race` cancel their losers; a cancelled frame runs its `defer` actions once
  and cannot catch the cancellation; `Cancelled` is data only at the boundary
  that owns the subtree.
- `serve.Config.max_in_flight` and lazy streaming request bodies, moved here
  from P1 (#9854).
- Exit: the two-fetch handler at 4,096 concurrent connections on one worker
  with p99 recorded against hyper; the blocking-handler conformance case
  passes; bump bytes per suspended handler recorded; a `sim` test where a
  handler suspends on a scripted upstream and a scripted disconnect cancels
  it, byte-identical across targets.

The conformance case P1 was to leave failing ("a handler that blocks for
100 ms is documented as stalling its worker") was never written; slice 1 adds
it.

## 2. Where the code stands (2026-10-04)

- `std/async` is a library. `Future[T]` is `Ready(T)` or `Pending(token,
  resume)`; `gather_on`, `race_on` and `with_deadline_on` are blocking poll
  loops over `Driver.poll_ready`. There is no scheduler, no task queue, no
  compiler support beyond builtin signatures.
- `std/serve`'s loop owns the worker's `RealDriver` (epoll or kqueue through
  `reactor_new` / `reactor_ctl` / `reactor_wait`), reads a whole request
  before calling the handler, and writes the response with writability
  interest for the unsent tail. `Host { reactor, pool }` is built once per
  loop; `Host.reactor` is stored and never read, and `Host.http` blocks in
  `tcp_recv_deadline` through `fetch.send_public_on`.
- Both IRs are structured stack machines (`OpBlock` / `OpLoop` / `OpIf` /
  `OpBr`), not CFGs: `internal/ir` natively, `examples/self_host/ir.fern`
  below the typed pipeline (`semsource` → `ssasem` → `ssarc` → stack IR).
  Perceus is emitted during lowering, so a pass over the stack IR sees
  `OpRcInc` / `OpRcDec` already in the stream.
- `defer` and `errdefer` are inlined at every exit (`collectDefers`,
  `emitDeferCleanupKind`), with an active flag per defer. Nothing registers
  cleanup at runtime.
- `effects.Build` gives the call graph over the checked AST; `ambient.Enforce`
  (E080) already walks it every build, treating `platform.Platform` impl
  methods as leaves.
- A native-only leak: `genEnumDropFn` skips closure payloads held in enum
  boxes, citing `Future.Pending`'s resume closure, and the match arm that
  binds one takes no count on it, so every enum-held closure leaks its pair
  and env on native whether it is matched or not (two blocks per value,
  measured under `FERN_LEAKCHECK`). The self-host releases them (its
  `enum_walk_payload_field` walk counts a closure payload at construction and
  decs it on drop), so P3's self-host-built binaries are not affected; the
  native fix is its own bugfix PR, not a P3 slice.
- Native is frozen (`NATIVE-FREEZE.md`): `internal/` takes bugfixes, oracle
  needs and what the self-host sources need to bootstrap. New language
  mechanics land self-host first.

## 3. Design

### 3.1 One suspension primitive, not a classifier over effects

#9856 describes the classifier as "functions that transitively reach a
`Platform` I/O effect". Taken literally that is the wrong set: `plat.log`
never waits, and the serve loop's own `drv.wait` is the scheduler and must not
be transformed. The thing a handler waits on is one operation: *park this
computation until a readiness token fires*. So the design has one primitive,

```
__suspend(token: i32): i32     // returns the readiness word, or the cancel mark
```

and the suspendable set is exactly the functions that reach `__suspend`
transitively in the monomorphised call graph. `effects.Build` already computes
that graph; the classifier is a reachability query on it with `__suspend` as
the only source. `handle[P: platform.Platform]` instantiated at `Host` or
`SimPlatform` is in the set; at `MockPlatform` it is not, because the mock
answers from a table. The serve loop is not in the set: it calls `reactor_wait`
directly and never `__suspend`.

Only the stdlib calls `__suspend`, in one place: the wait helper the fetch
client already funnels its connect, DNS, send and receive waits through. A
handler that calls `plat.http` suspends without a word of syntax, which is the
colorless property #9856 keeps.

### 3.2 Outside a scheduler the primitive blocks, so nothing else changes

`__suspend` has two behaviours, chosen at runtime by whether a task is being
driven:

- Under a scheduler (the serve loop, the sim driver), it unwinds the task and
  hands the token up; the scheduler rewinds the task when the token fires.
- With no task being driven (`main` on a CLI, a test, a wasi-http instance
  that serves one request), it is `poll([token], -1)` followed by return:
  today's blocking wait.

This is what lets the stdlib adopt the primitive everywhere at once without
a second copy of the client for programs that never serve. It is also the
fallback that keeps the frozen native compiler honest (§3.6).

### 3.3 The lowering: unwind and rewind on the structured IR

Rust and C# build state machines on a CFG. Fern's IR is wasm-shaped, and the
technique for suspending structured code without a CFG is Binaryen's
Asyncify, which applies here unchanged in shape:

- Each function in the suspendable set gets a *call index* per call site
  that may suspend (a call to another suspendable function, or to
  `__suspend` itself).
- **Unwind.** When `__suspend` is reached under a scheduler, it sets the
  task's mode to *unwinding* and returns a dummy. Each transformed frame on
  the way out sees the mode, appends its live locals and the call index it
  was at to the task's save area, and returns a dummy to its caller. The
  outermost transformed frame returns to the scheduler, which now holds the
  token and a save area describing the whole suspended stack.
- **Rewind.** The scheduler calls the entry function again with the mode set
  to *rewinding*. Each frame restores its locals from the save area, skips
  the code before its saved call index, and re-enters that call; the
  innermost frame reaches `__suspend`, which clears the mode and returns the
  readiness word. From there execution is ordinary.
- Skipping in structured code is the guarded re-walk Asyncify uses: every
  straight-line segment of a transformed function is wrapped in
  `if (!rewinding)`, and the block and loop structure is re-entered as
  written, so no new control-flow form is added to either IR.

The pass runs on the stack IR after Perceus has emitted its ops, in both
compilers, so a saved local that owns a reference keeps that reference in the
save area and the restore puts it back before any `OpRcDec` that names it. No
RC op is moved. The save area is a heap object owned by the task, which is
why the two source rules in §3.5 exist.

Only the suspendable set pays for the transform. The request parse and the
response serialise in `std/serve` never reach `__suspend` and keep their
code and instruction counts; what grows is the fetch client's wait path and
the handlers that call it. The size and instruction deltas are measured in
slice 2 before anything depends on them.

### 3.4 Tasks, the scheduler in `std/serve`, and cancellation

`async.Task` is the unit a scheduler drives: the save area, the mode word,
the token it waits on, and the entry closure. Its API is three functions:

- `task_start(entry) → Status` runs the entry until it returns or suspends.
- `task_resume(t) → Status` rewinds and runs to the next suspend or return.
- `task_cancel(t) → Status` rewinds in *cancel* mode: `__suspend` returns the
  cancel mark, the lowering treats it as an uncatchable early return at that
  call, so the frame runs its `defer`s and its ordinary exit path (which is
  where its `OpRcDec`s are), and each caller does the same up to the entry.

`Status` is `Done(result)`, `Suspended(token)` or `Cancelled`. Because cancel
is a rewind that takes every normal exit path, cancellation needs no separate
drop routine per suspend point, and the Perceus accounting of a cancelled
task is the same accounting as a returned one. A task is never discarded
without that rewind: `Task`'s drop runs `task_cancel` if the task is still
suspended, which is what reclaims a `race` loser once slice 6 makes the
combinators suspend.

Only one task runs at a time per worker, so the "current task" the lowering
consults is a per-execution-context slot in the runtime, beside the allocator
state: not Fern module state (R3.1 holds), and on bare metal it is per
context the way the refcount question in `BARE-METAL-PLAN.md` is.

The serve loop becomes a scheduler:

- `handler(req, plat)` becomes `task_start`. `Done` responds as today.
  `Suspended(token)` records the task against the connection, watches the
  token's fd on the worker's reactor, and goes back to `drv.wait`. A
  readiness event on a handler-owned fd resumes that task; `Done` responds;
  `Suspended` re-arms.
- `max_in_flight` bounds the suspended tasks per worker; at the cap the loop
  stops reading further requests (the same backpressure shape as
  `max_connections` unwatching the listener). Requests on one connection stay
  serialised: a pipelined request starts after the previous response.
- A client disconnect on a connection whose task is suspended cancels the
  task before the connection is closed. The timer path for `with_deadline`
  inside a handler is a timer token like any other.
- `Host.http` watches its sockets on `Host.reactor`, the field that has been
  waiting for this, and suspends on them.

### 3.5 The two source rules

Both from #9856, both checker errors in both compilers, both over the same
reachability result as §3.1 (so they run where E080 runs, after
monomorphisation):

- **No `str` or `[u8]` view live across a suspension point.** A view is a
  borrowed window onto a buffer that the save area cannot keep alive
  (`STR-VIEW-CONTRACT.md` §5 keeps views out of every field position, and a
  save area is fields). A local of view type that is live after a call to a
  suspendable function is refused by name, with the hint to materialise it
  (`to_string()`, `to_array()`) before the call. A streaming body view is
  consumed or materialised before the handler suspends.
- **A closure crossing a suspension point captures by value.** Reference
  captures share the pointee with the enclosing frame
  (`CLOSURE-CAPTURE.md`); after a rewind the frame's locals are restored
  copies, so a shared cell would split. Inside a suspendable function a
  closure that captures a mutable local by reference and is live across a
  suspendable call is refused, as the spawn closure is in
  `MULTICORE-RESEARCH.md` C5.

Slice 7 fixes the codes and the exact wording; the checker-codes
differential pins both compilers to the same set.

### 3.6 Which compiler does what

The transform lands in the self-host compiler, on the stack IR shared by its
x86-64, arm64 and wasm emitters, so one pass serves every target. Under the
freeze, native gets the primitives but not the transform: `__suspend`,
`task_start`, `task_resume` and `task_cancel` are builtins whose native and
interp lowering is the blocking fallback of §3.2 (`task_start` runs the entry
to completion; `__suspend` polls). That keeps every stdlib module compiling on
native and keeps the differential suites meaningful, since a program that
never multiplexes behaves identically either way. The multiplexing and
cancellation gates are self-host-built binaries, the way the sim wasm legs
are today (#9854). "Native" and "wasm" in #9857's exit criteria are met by
the self-host compiler's x86-64, arm64 and wasm output.

If the owner wants the transform in `internal/ir` as well, it is one more
slice argued on #4451, and §3.3 is written so the port is mechanical. The
plan does not depend on it.

The interp keeps its oracle role for the sequential semantics. If a cancel
gate needs an interp leg, the interp implements tasks with a goroutine per
task and a hand-off channel, which is an implementation detail invisible to
the program.

### 3.7 wasm

The transform is target-agnostic, so wasm gets it with the others. Tokens are
pollables, as `poll` already takes them; the serve loop on wasi:sockets drives
tasks exactly as native does. The Preview 3 `waitable-set.wait` loop in
`wasmbin/extern.go` stays stackful for now; once tasks exist, a callback-lifted
guest that suspends across `waitable-set.wait` is a follow-up on
`WASI-PREVIEW3-ASYNC-PLAN.md`, not part of P3.

### 3.8 The sim

`SimPlatform.http` suspends on a virtual-time token, as `sim_fetch` already
produces them. `sim.Sim` grows a task driver (`run_tasks`) that advances
virtual time and resumes the task whose token is due, and a scripted
disconnect at a virtual time is a `task_cancel`. That gives the deterministic
cancellation test #9857 asks for, with the bytes pinned in the test.

## 4. Slices

Each slice is one PR with its gates; the self-host suites are primary
throughout (`TEST-GATES.md`: the fixpoint is blind to a stable miscompile).

1. **Groundwork.** The blocking-handler conformance case in the serve
   harness: a one-worker server whose handler waits on `plat.http` to an
   upstream that answers after 100 ms, and a second connection's hello
   measured against it. It lands asserting the stall P1 documented (the
   hello waits out the upstream), so the suite is green, and slice 5 flips
   the assertion to the answer arriving inside the wait.
2. **The primitive and the transform, self-host.** `__suspend`, `Task` and
   the three task builtins; the reachability classifier over
   `effects.Build`; the unwind/rewind pass on `ir.fern`. Gate: a program
   where a function three calls deep suspends twice inside a loop and a
   branch, driven by a hand-written scheduler in the test, on x86-64, arm64
   and wasm through the self-host CLI; the same program under no scheduler
   returns the same value through the blocking fallback. Code size and
   instruction count of the transformed functions recorded in the PR.
3. **Native and interp fallback.** The four builtins classified in
   `internal/platforms`, `internal/caps` and both self-host mirrors; blocking
   lowering on native, interp and wasmbin; the differential rows.
4. **The client suspends.** `fetch.Sockets` carries the reactor it registers
   its sockets on, handed `Host.reactor` by `Host.http`, and the fetch wait
   helper calls `__suspend`; DNS, connect, send and receive waits go through
   it. Gates: `TestFetchClient`
   and its twin unchanged in output; the self-host serve twin shows one
   handler's `plat.http` wait overlapping another connection's request.
5. **The serve loop drives tasks.** In-flight table, fd-to-task map,
   `max_in_flight`, disconnect cancels, per-connection serialisation. The
   slice-1 conformance case flips to pass. The two-fetch handler at 4,096
   connections joins `net-nightly` with p99 against hyper, and bump bytes
   per suspended handler join the held-connection heap gate.
6. **Cancellation semantics.** Cancel-mode rewind runs `defer`s; `Task`'s
   drop cancels; `gather_on`, `race_on` and `with_deadline_on` suspend on
   their token set under a scheduler instead of polling, and a `race` loser
   is cancelled rather than abandoned. `Cancelled` appears at the
   `with_deadline` slot and the `race` loser. `ASYNC.md` §8 and its
   limitations list are rewritten.
7. **The two source rules.** Both checkers, both codes, the differential and
   the checker-codes rows.
8. **Sim parity.** `SimPlatform.http` suspends; `sim.run_tasks`; the scripted
   upstream plus scripted disconnect test, byte-identical on x86-64, arm64
   and wasm through the self-host compiler.
9. **Lazy streaming request bodies.** `BodyStream` pulled inside a handler
   suspends on the connection's readability; the P1 "bodies are read before
   the handler runs" restriction is lifted behind a `serve.Config` choice,
   with the body caps and minimum data rate still enforced by the loop.
10. **Docs and reference.** `ASYNC.md`, `STDLIB.md` (serve, platform,
    async), the tutorial's handler section, `TEST-GATES.md` rows, and the
    `docs/README.md` entry for this file flipped to [record].

## 5. Risks and open questions

- **Transform cost.** Asyncify-style guards inflate transformed code; the
  measurement in slice 2 decides whether the fetch client's wait path needs
  narrowing (fewer functions between the handler and `__suspend`) before
  slice 5 relies on it.
- **Views in the stdlib's own wait path.** Slice 7's rule applies to the
  stdlib first; the fetch client and the HTTP parser hold `[u8]` windows in
  places that may now sit across a suspend. Slice 4 materialises them where
  it finds them and records what it cost.
- **The owner's call on native.** §3.6 keeps native on the blocking fallback.
  If the multiplexing gates are wanted on Go-built binaries too, the
  `internal/ir` port is argued on #4451 as its own slice.
- **Interleaving per connection.** One task per connection at a time is the
  simplest rule and matches HTTP/1.1 ordering; HTTP/2 (P6) will want several,
  and the in-flight table is keyed to allow it.
