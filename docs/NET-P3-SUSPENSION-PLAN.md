# Networking P3 — inferred suspension (plan)

> Status: **in progress, 2026-10-04.** Phase P3 of #9851, tracked in #9857.
> The decision it implements is #9856 (B): functions that reach a platform
> wait are lowered to resumable state machines at the IR level, the surface
> stays colorless, the worker's reactor drives them. This document says what
> that costs, where each piece lives, and in what order it lands. §4 marks
> the slices that have landed.

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

and the suspendable set is exactly the functions that reach it transitively
in the monomorphised call graph. As built, the primitive is
`__task_park(set, timeout_ms)`, reached only through
`async.wait_any(set, timeout_ms)`, which blocks on the set instead when no
task is current; the classifier (`suspend.classify`) is a
reachability query over the lowered rows with the park as its one source, the
same closure `semlower.pure_rows` takes for purity. `handle[P: platform.Platform]` instantiated at `Host` or
`SimPlatform` is in the set; at `MockPlatform` it is not, because the mock
answers from a table. The serve loop is not in the set: it calls `reactor_wait`
directly and never `__suspend`.

Only the stdlib calls `__suspend`, in one place: `async.wait_any`, which
the fetch client's connect, DNS and receive waits go through
(`tcp.tcp_recv_deadline`, `dns.first_readable` and `dns.readable_within`,
`dns.connect_race`). A handler that calls `plat.http` suspends without a
word of syntax, which is the colorless property #9856 keeps.

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
RC op is moved. The save area is a heap object owned by the task; §3.5 is
what that means for a view or a shared capture the frame holds.

Only the suspendable set pays for the transform. The request parse and the
response serialise in `std/serve` never reach `__suspend` and keep their
code and instruction counts; what grows is the fetch client's wait path and
the handlers that call it. The size and instruction deltas are measured in
slice 2 before anything depends on them.

### 3.4 Tasks, the scheduler in `std/serve`, and cancellation

`async.Task` is the unit a scheduler drives: the save area, the mode word,
the wait it parked on (the (fd, interest) pairs and the bound `wait_any`
was given), and the entry closure. Its API is three functions:

- `task_start(entry) → Status` runs the entry until it returns or suspends.
- `task_resume(t) → Status` rewinds and runs to the next suspend or return.
- `task_cancel(t) → Status` rewinds with the task marked cancelled: the
  park answers `async.cancelled()` and every later wait answers it at once
  without parking, so the frame cannot wait again; it winds down through its
  own control flow (a `plat.http` that returns `Err(Cancelled)`, the handler
  returning), which runs its `defer`s and its ordinary exit path, where its
  `OpRcDec`s are, and each caller does the same up to the entry.

`Status` is `Done(result)`, `Suspended(wait)` or `Cancelled`. Because a
cancelled task leaves through its normal exit paths, cancellation needs no
separate drop routine per suspend point, and the Perceus accounting of a
cancelled task is the same accounting as a returned one. What #9856 called
"resumed once, uncatchable" holds in the form that matters: the task cannot
block again, and nothing it does can make the scheduler wait on it. A task
is never discarded without that rewind by the code that owns one: the serve
loop cancels a flight whose connection went away, and the task combinators
(`gather_tasks`, `race_tasks`, `with_deadline_tasks`, slice 6) cancel a
`race` loser, a child past the deadline, and every child of a task that is
itself cancelled. Tasks nest for that: a combinator inside a task starts
its children as tasks of their own, and `__task_leave` makes the enclosing
task current again.

Only one task runs at a time per worker, so the "current task" the lowering
consults is a per-execution-context slot in the runtime, beside the allocator
state: not Fern module state (R3.1 holds), and on bare metal it is per
context the way the refcount question in `BARE-METAL-PLAN.md` is.

The serve loop becomes a scheduler:

- `handler(req, plat)` becomes `task_start`. `Done` responds as today.
  `Suspended(wait)` records the task against the connection, watches each
  pair of `wait.set` on the worker's reactor with its interest, keeps
  `wait.timeout_ms` as the task's deadline, and goes back to `drv.wait`. A
  readiness event on a handler-owned fd resumes that task with the pair's
  index, the deadline with -1; `Done` responds; `Suspended` re-arms.
- `max_in_flight` bounds the suspended tasks per worker; at the cap the loop
  stops reading further requests (the same backpressure shape as
  `max_connections` unwatching the listener). Requests on one connection stay
  serialised: a pipelined request starts after the previous response.
- A client disconnect on a connection whose task is suspended cancels the
  task before the connection is closed. The timer path for `with_deadline`
  inside a handler is a timer token like any other.
- `Host.http` needs nothing of its own: the fetch client's waits are
  `wait_any` calls, and the scheduler watches what the park hands back.
  `Host.reactor` stays the loop's handle for what the worker itself watches.

### 3.5 What a parked frame keeps (the two source rules, withdrawn)

#9856 asked for two checker rules over the reachability result of §3.1: no
`str` or `[u8]` view live across a suspension point, since a view is a
borrowed window onto a buffer a save area cannot keep alive, and no closure
capturing a mutable local by reference live across one, since a rewound
frame's locals would be restored copies and the shared cell would split.
Neither hazard exists in the lowering slice 2 landed, so neither rule does:

- An unwinding frame saves its locals as the words they are and returns
  without running its exit-path releases, so every reference the frame
  holds keeps its count in the save area, and the owner a view borrows from
  survives the park with it. A view whose owner is not a local of some
  frame on the chain is the general dangling-view hazard the view contract
  already covers, park or no park.
- A scalar a closure captures and either side assigns is a heap cell both
  sides point at (`closureconv.BoxMutatedCaptures`, `cellify_env` in the
  self-host); the frame's word is the cell's address, which the save area
  keeps and the rewind restores. A reference capture cannot be reassigned
  at all (E049).

`TestSelfHostTaskFrame` (`e2eharness.TaskFrameProgram`) pins it: a parked
function reads a `str` of a parameter's string, a `str` of its own local
string, a byte view and a counter closure after the park, and answers what
the plain run answers. A lowering that stopped keeping either would fail
that gate, and that is the point at which a rule would be worth its
refusals.

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

The transform is target-agnostic, so wasm gets it with the others, and the
runtime it needs is `wasm_ir.task_funcs`: the WAT twin of `rt_src_task`,
with the task table, the records and the save area in linear memory and the
current task in a global. A wait set's descriptors are socket handles,
subscribed for the wait as `wait_any` does; a park on a bound alone needs
nothing. The serve loop on wasi:sockets drives tasks exactly as native does.
The Preview 3 `waitable-set.wait` loop in `wasmbin/extern.go` stays stackful
for now; once tasks exist, a callback-lifted guest that suspends across
`waitable-set.wait` is a follow-up on `WASI-PREVIEW3-ASYNC-PLAN.md`, not part
of P3.

### 3.8 The sim

`SimPlatform.http` parks on a virtual-time token: every move of the sim's
clock goes through `Sim.advance_to(to_ns)`, which under a task is a
`wait_any` on the token `to_ns` in milliseconds (the sim's token encoding)
with no bound, and `Sim.poll_ready` parks on its tokens as `RealDriver`'s
does. `sim.run_tasks(drv, entries, cancel_at_ms)` is the task driver: it
starts every entry as a task, moves the clock to the earliest moment any
parked task is due — a pair's time, its bound's end, or its scheduled
cancellation — and resumes that task with the pair's index or -1, or
cancels it; a scripted disconnect is a `task_cancel` at a virtual time,
winning a tie with the data it would have read. Ties between tasks are the
seeded PRNG's, so a run is a function of the seed.

The transport reports the cancellation rather than swallowing it:
`Transport.read` answers `tcp.Recv` — `Chunk`, `Elapsed`, or `Abandoned`
when the task's wait answered `cancelled()` — and `tcp_recv_deadline` is
that primitive on a socket; `connect_race` leaves the race as
`Interrupted` on a cancelled wait instead of spinning on it until the
deadline. The client maps both to `FetchError.Cancelled`, so a handler's
`plat.http` sees the disconnect as an error it can log, on the sim and on
the host alike. `TestSelfHostSimTasks` is the deterministic cancellation
test #9857 asks for, byte-identical on x86-64, arm64 and wasm.

## 4. Slices

Each slice is one PR with its gates; the self-host suites are primary
throughout (`TEST-GATES.md`: the fixpoint is blind to a stable miscompile).

1. **Groundwork.** The blocking-handler conformance case in the serve
   harness: a one-worker server whose handler waits on `plat.http` to an
   upstream that answers after 100 ms, and a second connection's hello
   measured against it. It lands asserting the stall P1 documented (the
   hello waits out the upstream), so the suite is green, and slice 5 flips
   the assertion to the answer arriving inside the wait.
2. **The primitive and the transform, self-host. Landed for the native
   targets.** The nine `__task_*` primitives (`internal/stdlib/std/async.fern`
   writes `Task`, `task_start` / `task_resume` / `task_cancel` and `suspend`
   over them), the runtime in `asmcore.rt_src_task` over a `.bss` state block,
   the classifier and the unwind/rewind pass in
   `examples/self_host/suspend.fern`, run from `ssarc.lower` before the
   peepholes on the rows `semlower` marks. Gate: `TestSelfHostTaskScheduler`
   (a function three calls deep parks twice inside a loop and a branch, driven
   by hand, x86-64 and arm64) and `TestTaskSchedulerFallback` (the Go
   compiler's blocking fallback). The Go compiler and the interp carry
   the primitives in the blocking fallback, so every stdlib module compiles
   everywhere. wasm's runtime bodies (`wasm_ir.task_funcs`, the WAT twin of
   `rt_src_task`, every word 8 bytes so a saved i64 or f64 keeps its width)
   landed after slice 6: `TestSelfHostTaskPortable` parks the same program
   on x86-64, arm64 and wasm, composed with `cmd/fern/wit`'s world so the
   list-returning `wasi:io/poll poll` import lifts. The classifier's
   indirect-call rule has no closure body reaching a park yet to exercise
   it.
3. **Native and interp fallback.** Done with slice 2 for the primitives; the
   differential rows follow with the first stdlib caller of `suspend`.
4. **The client suspends. Landed.** `async.wait_any(set, timeout_ms)` is
   the wait the park carries, (fd, interest) pairs under a bound, which the
   scheduler reads back as `Suspended(Wait)`. `tcp.tcp_recv_deadline`, the
   DNS exchange's receive waits and `dns.connect_race`'s connect wait go
   through it, so a `fetch.send` inside a task parks at each; `tcp_send`
   stays a blocking write. The interpreter's `poll` became a set over its
   handles rather than a stub, and the elapsed-time heuristics that told
   the stub from a timeout went with it. Gates: `TestFetchClient` and its
   twin unchanged in output; `TestSelfHostFetchTask` runs a fetch inside a
   hand-driven task and fetches again while it is parked;
   `TestFetchTaskFallback` is the Go compiler's blocking twin. The serve
   twin that shows one handler's wait overlapping another connection's
   request is slice 5's, with the scheduler.
5. **The serve loop drives tasks. Landed for the stateless loop.** Each
   request's handler runs as a task; one that parks becomes a flight (its
   task, its connection, the pairs it waits on watched on the worker's
   reactor, its bound as a due time, and what its response needs), and the
   loop goes back to its wait. A readiness event on a flight's descriptor
   resumes it with the pair's index, its due time with -1; `Done` answers
   on the connection outside any burst and leaves a framed request behind
   it as backlog, `Suspended` re-arms, `Cancelled` closes. A connection
   with a flight takes no further request until it answers (pipelined
   requests stay in order), reads what the peer sends meanwhile, remembers
   the end of its stream, and a hang-up on it cancels the flight.
   `Config.max_in_flight` (1024) gates the listener as `max_connections`
   does, and a stopping loop cancels every flight as it ends. The
   slice-1 conformance case flipped to pass (`TestSelfHostServeHandlersOverlap`).
   The stateful loop (`run_with`) still runs its handler to completion:
   its state threads through the handler chain, so a parked handler would
   hold it from every other request. Still open from this slice: the
   two-fetch handler at 4,096 connections in `net-nightly` with p99 against
   hyper, and bump bytes per suspended handler in the held-connection heap
   gate.
6. **Cancellation semantics. Landed.** `gather_tasks`, `race_tasks` and
   `with_deadline_tasks` run their entries as tasks of the calling task and
   drive them together: the combinator parks on the union of its children's
   waits and resumes the child whose pair became ready or whose bound
   passed; a `race` loser, a child past the deadline, and every child of a
   task that is itself cancelled are cancelled through `task_cancel`, so
   each leaves through its own exit paths and its `defer`s run. Tasks nest:
   `__task_enter` keeps the task that was current and `__task_leave` makes
   it current again. `RealDriver.poll_ready` parks under a task, so the
   `Future` combinators (`gather`, `race`, `with_deadline`) inside a handler
   park the handler too. Under the blocking fallback the entries run to
   their end in order inside `task_start`. Gates: `TestSelfHostTaskCombinators`
   (`e2eharness.TaskCombinatorsProgram`: a race whose loser is cancelled, a
   gather, a deadline that cancels the late entry, a future gather, and a
   task cancelled from outside while its race is parked, every entry with a
   `defer`) and `TestTaskCombinatorsFallback`. Not in this slice: `Task`'s
   drop cancelling a still-parked task. `core/mem.Drop` runs inside the drop
   glue, and running a task's remaining code from there is not a place to
   run user code; a `Task` dropped while parked keeps its record until
   `task_free`, and the combinators cancel and free their children
   themselves.
7. **The two source rules. Withdrawn (§3.5).** Views and shared mutated
   captures survive a park as the lowering stands, so no checker rule
   refuses them; `TestSelfHostTaskFrame` and `TestTaskFrameFallback` pin
   that instead of a code.
8. **Sim parity. Landed (§3.8).** `Sim.advance_to` parks, `sim.run_tasks`
   drives, `Transport.read` reports a cancelled task; `TestSelfHostSimTasks`
   is the scripted upstream plus scripted disconnect test, byte-identical
   on x86-64, arm64 and wasm through the self-host compiler, and
   `TestSimTasksFallback` the Go compiler's twin.
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
