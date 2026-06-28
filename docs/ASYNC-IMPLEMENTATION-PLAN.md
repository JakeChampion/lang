# Async / concurrency implementation plan

Companion to the research docs `ASYNC-IMPLEMENTATION-RESEARCH.md`
(Koka / Lean 4 / Roc / AOT+WASM mechanics) and
`CONCURRENCY-RESEARCH.md` (the menu + the chosen surface). Those
decided *what* to build; this doc decides *how*, sequences the work
into shippable slices, and records what the first slice already
landed.

## The decision, in one paragraph

Fern gets **colorless structured concurrency**, implemented as a
**stackless CPS / state-machine transform** driven by a
**single-threaded readiness reactor**. A `concurrent { … }` block
fans out `spawn`ed tasks whose I/O overlaps on one thread; `await`
joins them. There is **no function coloring** (suspension is a
property of the block, not the function signature), **no stackful
green threads** (they need per-arch assembly and have no WASM
story), and **no general algebraic-effect system yet** (effects are
the eventual colorless substrate, not the cheapest first step). This
matches `CONCURRENCY-RESEARCH.md`'s recommendation and the
mechanism analysis in `ASYNC-IMPLEMENTATION-RESEARCH.md`.

## Why an AST-level desugar (not an IR pass)

The single most important implementation choice. There are two
compilers and the Go one is being retired:

- **Go compiler** (`internal/`): a target-agnostic IR
  (`internal/ir`) with **structured control flow, not SSA** — a flat
  op list with `OpBlock`/`OpLoop`/`OpIf`. A true IR-level CPS
  transform here would mean flattening structured control flow into
  a state machine: hard, and **thrown away when the Go compiler
  retires**.
- **Self-hosted compiler** (`examples/self_host/`, the future): the
  default path goes **AST → asm directly** via the shared
  `asmcore.fern` frontend; there is *no* target-agnostic IR on that
  path (there is an optional SSA layer, `ssa.fern` →
  `ssa_{x86,arm64,wasm}.fern`, which is **becoming the default** —
  work in progress as of 2026-06).

Conclusion: lower async **at the AST level, desugaring into ordinary
Fern constructs** (a state struct, a state enum, continuation
closures, a driver loop). This is **IR-agnostic** — it survives
whether a backend is AST→asm or AST→SSA→asm, and whether SSA is on
or off — and it **rides machinery every backend already has**:
closures (capture = the task's live frame), enums, structs, and
loops all lower today on x86-64, arm64, and wasm32 in *both*
compilers. **No codegen, no IR, and no backend changes are required
for the core mechanism.** The only per-compiler frontend work is
lexer + parser (+ one checker rule); the desugar's *output* is
plain AST.

> Alternative noted, not chosen: once SSA-by-default lands for the
> self-hosted compiler, an SSA-level CPS transform becomes viable
> and would be shared across the self-host's x86/arm64/wasm SSA
> backends. It is still rejected as the *primary* path because (a)
> it would not benefit the Go compiler during the transition, (b)
> it duplicates a transform the AST desugar gives both compilers for
> free, and (c) the AST desugar is far easier to test (its output is
> readable Fern). Revisit only if the AST desugar proves to produce
> pathologically bad code that an SSA-level transform would avoid.

## The validated core shape

A task is uniformly a **`Step`**: stepping it either completes it
(`Done`) or suspends it on a reactor token, carrying a continuation
to resume with the woken value (`Wait`). The continuation is a
closure that **captures the task's live locals by value** — its
"stack frame" across the suspension point. This is the stackless-CPS
shape from the research, and it gives a **heterogeneous, generic-free
task interface**: every task is a `Step` regardless of internal
structure, so the runtime needs no generics, no `dyn`, and no
per-backend support.

```fern
pub enum Step {
    Done(i32),
    Wait(i32, (i32) => Step),   // token, resume(wokenValue) -> Step
}
```

**This compiles and runs unchanged on every backend** — verified in
Phase 0 (below): interp = 33, x86-64 = 33, arm64/qemu = 33, wasm
compiles (the recursive enum-through-function-type resolves and the
closure captures lower correctly everywhere). De-risking this
recursive type before committing was the gating spike.

What the eventual `concurrent { … }` desugar emits, by example —
the user writes:

```fern
concurrent {
    var a = spawn fetch(plat, url_a);   // suspends at the await inside fetch
    var b = spawn fetch(plat, url_b);
    return combine(await a, await b);
}
```

and the desugar produces (conceptually) one continuation per
suspension point, each a nested function capturing the live locals,
seeded into the scheduler — i.e. exactly the `Step` machinery in
`std/task`, generated rather than hand-written. The Phase-0 test
file `examples/tests/async_runtime_test.fern` *is* that output,
hand-written, so the desugar's target is proven before the desugar
exists.

## Where the impurity lives (the Roc lesson)

The **reactor lives in the platform-glue / stdlib layer**, not in
codegen — the "host owns the event loop" insight from Roc, kept
*in-binary* because Fern owns all backends (no host-ABI marshalling
cost). User code and the scheduler stay pure-ish and portable; the
one impure seam is the `poll` primitive plus non-blocking I/O, added
once per backend.

## Phased plan

Each phase is independently shippable with tests, per the engineering
bar ("every feature ships with tests"; "confirm passing tests before
a PR").

### Phase 0 — research, plan, runtime core — **DONE (this PR)**

- `ASYNC-IMPLEMENTATION-RESEARCH.md` (merged) + this plan.
- `std/task` Phase-1 runtime core: the `Step` vocabulary, an
  in-memory `Reactor` (token allocation + poll-drain), and a `run`
  driver that multiplexes single-await tasks and proves fan-out
  *overlap* (both tasks suspend before either resumes).
- `examples/tests/async_runtime_test.fern` — reactor unit tests +
  single-task resume + two-task fan-out + immediate-done, wired into
  the `internal/e2e` test-runner gate
  (`TestRunnerAsyncRuntimeExamplePasses`).
- Validated end-to-end on interp / x86-64 / arm64(qemu); compiles on
  wasm. **Zero codegen, IR, or backend changes.**

Limitation accepted in Phase 0, **lifted in Phase 2**: a continuation
`(i32) => Step` did not thread the `Reactor`, so a task could await at
most once before completing.

> **Sequencing note:** Phase 2 was implemented *before* Phase 1
> (reversing the original order). Rationale: Phase 1 writes ~6 codegen
> sites of `poll(2)`/`wasi:io/poll` assembly against the reactor
> interface; doing that *before* the multi-await generalization
> settled the interface would have risked reworking all that asm. So
> the runtime semantics (multi-await, the scheduler, the reactor's
> method shapes) were frozen in pure Fern first; Phase 1 now targets a
> stable interface.

### Phase 2 — generalize the runtime — **DONE**

- Continuations thread the `Reactor`
  (`(i32, Reactor) => (Step, Reactor)`), so a task can await any
  number of times (sequential dependent I/Os), not just once.
- A real multi-round scheduler: each `run` round polls the reactor,
  resumes every task whose token completed (each may register a
  further wait), and repeats until all tasks are `Done`. Waits
  registered mid-run are picked up on a later round. The parked set
  is the task-states array itself (`Step[]`), scanned by token — no
  separate waiter map needed at this scale.
- Verified end-to-end (multi-await + mixed-depth + fan-out): interp /
  x86-64 / arm64(qemu) = correct; wasm compiles. Still **zero
  codegen/IR/backend changes** — pure Fern.
- Results stay i32/pointer-concrete; generic `Task[T]` remains gated
  on self-host monomorphization (below).

### Phase 1 — the real reactor (the hard plumbing)

Replace the in-memory reactor internals with real readiness, behind
the same `std/task` API shape. This is the one phase that touches
backends — one new primitive plus non-blocking I/O — in **both**
compilers' codegen.

**Phase 1a — DONE (the x86-64 `poll` primitive):** the
`poll(fds: i32[], timeout_ms): i32` builtin lowers on x86-64 (Go
compiler) to `__fern_poll` — marshals the length-prefixed `i32[]` into
a `struct pollfd[]` (POLLIN per fd), calls `poll(2)` (#7), and returns
the index of the first readable fd (or -1). Checker sig + call-site
flag + target map + the runtime helper (`emitPollRuntime`). Verified
with deterministic file-fd tests (`internal/e2e/poll_x86_test.go`:
single-ready → 0, two-ready → 0, empty → -1). Parity-neutral: nothing
wires the free `poll` builtin yet, so existing programs are unaffected.

**Phase 1b — DONE (native arm64 `ppoll`):** the same `poll` builtin
lowers on arm64 (Go compiler) to `__fern_poll` via `ppoll(2)` (#73 —
arm64 has no bare `poll`; `timeout_ms` < 0 → NULL timespec = block,
>= 0 → a built timespec). arm64-darwin returns a -1 stub pending its
kqueue path. Same deterministic file-fd tests now run on both native
backends (`internal/e2e/poll_test.go`, x86-64 + arm64/qemu).

**Phase 1c — IN PROGRESS:**
- **DONE — `std/reactor` (native real-fd scheduler):** `run_io(states)`
  drives fd-tagged stackless tasks (`IoStep = IoDone | IoWait(fd,
  resume)`) to completion using the real `poll` builtin — the real-I/O
  counterpart to `std/task`'s in-memory reactor. Kept in a separate,
  native-only module so `std/task` (imported by the `concurrent`
  desugar) never depends on a native-only builtin and stays compilable
  on wasm/interp. Verified on x86-64 + arm64/qemu with deterministic
  file-fd tasks (`internal/e2e/reactor_test.go`).
- **wasm:** `wasi:io/poll.poll(list<pollable>) -> list<u32>` — a
  *different shape* than raw fds (pollables, not ints), so the wasm
  reactor exposes the same `run_io`-style API over pollables rather
  than reusing the `poll(fds)` builtin. In `internal/codegen/wasmbin`
  (Go) + `wasm.fern` (self-host).
- **DONE — `timer_fd(ms)` builtin (native):** a CLOCK_MONOTONIC
  timerfd readable after `ms` (`timerfd_create`/`timerfd_settime`;
  x86-64 #283/#286, arm64 #85/#86; Darwin -1 stub). Gives the reactor
  a real wait→ready transition for a deterministic readiness test
  (`internal/e2e/reactor_test.go ▸ TestReactorTimers`: two timers via
  `run_io`, the shorter serviced first — proving `poll` actually
  blocks on real readiness, not just always-ready files) and is the
  primitive for async **timeouts** (Phase 5).
- **DONE — reactor timeouts (`run_io_deadline`):** bounds the whole
  fan-out by a wall-clock deadline (poll's timeout + `monotonic_ns`
  across rounds); tasks that don't finish in time are abandoned (-1),
  like a cancelled `select` loser. Pure Fern, no new builtin. This is
  the Phase-5 timeout / bounded-happy-eyeballs primitive. Tested both
  paths (completes / times out) on x86-64 + arm64 with deterministic
  timerfds (`internal/e2e/reactor_test.go ▸ TestReactorDeadline`).
- **DONE — `poll` validated on real TCP sockets:** a poll-driven
  one-shot server (poll the listener → accept → poll the connection →
  recv → respond) round-trips a real localhost request end-to-end
  (`internal/e2e/reactor_socket_test.go`). No new builtins — a blocking
  accept/recv doesn't block once `poll` reports the fd ready, so the
  reactor needs no non-blocking variants for this shape. This is the
  edge-handler serving path over the reactor.
- **DONE — `tcp_connect` (x86-64) + the outbound fan-out (trigger
  condition):** added the outbound client primitive
  `tcp_connect(host_be, port)` (socket + sockaddr_in + connect(2),
  mirroring `tcp_listen`; host as a network-order-packed i32). The
  literal `CONCURRENCY-RESEARCH.md` trigger — *two parallel fetches,
  await both* — now runs end-to-end over the native reactor: a program
  opens two outbound connections and multiplexes their responses
  through `std/reactor.run_io`
  (`internal/e2e/reactor_socket_test.go ▸ TestReactorOutboundFanoutX86_64`,
  Go upstream answering both). This is the edge-handler fan-out
  (fetch cache + primary, take both) working for real.
- **DONE — arm64 `tcp_connect`:** mirrors the x86 helper (socket +
  sockaddr_in + connect(2) #203 / Darwin #98), so the outbound fan-out
  test now runs on **both** native backends (arm64 under qemu connects
  to the host upstream). Native parity for the full edge-handler loop
  (serve + fan-out fetch).
- **DONE — `std/fetch` (outbound HTTP client):** `fetch_get(host_be,
  port, path)` + `fetch_raw` + `http_body` + `ipv4(a,b,c,d)` — a
  blocking HTTP/1.1 GET over `tcp_connect`/`send`/`recv` returning the
  real response string. The upstream-fetch capability a handler needs.
  Verified on x86-64 + arm64 against a Go upstream
  (`internal/e2e/fetch_test.go`).
- **DONE — `plat.fetch` capability method:** `(plat: Platform)
  fetch(host, port, path)` (+ `parse_ipv4`), so a handler reaches
  upstreams *only* through the `Platform` bag it was handed
  (docs/PLATFORM-RESEARCH.md Rec §1's capability model) — a literal
  IPv4 GET returning the response. Verified on x86-64 + arm64 against a
  Go upstream (`internal/e2e/fetch_test.go ▸ TestPlatformFetch`).
- **DONE — reactor-overlapped fan-out returning bodies:** the complete
  "two parallel fetches, return both response bodies" payoff. Now over
  a single **generic** `IoStep[T]` reactor (`run_io[T]` /
  `run_io_deadline[T]`): the i32 fan-out uses `IoStep[i32]`, the
  body-returning fan-out `IoStep[string]`. This previously needed a
  hand-duplicated string twin (`IoStepStr`/`run_io_str`) because the Go
  checker mis-inferred `T` through the function-typed `IoWait` payload;
  that inference gap is now **closed**
  (docs/GENERIC-VARIANT-FN-PAYLOAD-INFERENCE-GAP.md — a positional
  type-arg/payload pairing in `postSettleType`), so the twins folded
  into the one generic shape. Verified x86-64 + arm64 (two overlapped
  fetches → both bodies, `TestReactorFanoutBodies`).
- **Self-host** (`asm.fern` / `asm_arm64.fern`) — mirror the reactor
  builtins (blocked on the fn-payload-variant gap, #3552, for
  `std/reactor` itself).
- Non-blocking accept/recv/send — only for partial-read / spurious-
  wakeup correctness; the poll-then-blocking shape covers the common
  case.

Risk: this is the most backend-heavy slice and the only one needing
arm64/qemu + wasmtime in the loop. Gate locally on x86-64 + interp +
wasm; let CI run arm64.

### Phase 2.5 — remaining runtime generalization (deferred)

- **Generic `Task[T]`** instead of the i32/pointer-concrete core.
  *Gated on self-host generic monomorphization* — the self-hosted
  compiler currently does generic *erasure* with known field-dispatch
  gaps (see `SELF-HOST-AUDIT.md`), so until monomorphization lands
  the runtime stays concrete to keep it compiling on the self-hosted
  path. (The Go compiler already monomorphizes; this is purely a
  self-host-readiness gate.) Pick up when monomorphization lands.
- If the linear token scan ever shows up (many concurrent waits in
  one handler — unlikely for edge fan-out), swap the `Step[]` scan
  for a `core/map` waiter index. Not needed at current scale.

### Phase 3 — the surface syntax (`concurrent` / `spawn` / `await`)

The user-facing slice. Parser-time desugar, **emitting the Phase-2
`Step`/scheduler shape**, so no codegen changes.

- Lexer: add `concurrent`, `spawn`, `await` keywords — in
  `internal/lexer/lexer.go` **and** `examples/self_host/lexer.fern`.
- Parser: `parseConcurrent` (block), `spawn EXPR`, `await EXPR` — in
  `internal/parser/parser.go` **and** `examples/self_host/parser.fern`.
  Model on the existing `for..in` and `use <-` desugars, which
  already build synthetic AST (nested functions, rewritten calls) at
  parse time.
- AST: `*ast.Concurrent`, `*ast.Spawn`, `*ast.Await` (Go) + parallel
  union variants (Fern).
- Checker: `await e` requires `e : Task[T]`, yields `T`; `spawn` in a
  `concurrent` scope yields a `Task[T]`; scope-bounded — a `Task`
  may not escape its `concurrent` block (structured concurrency).
  One rule, in `internal/checker` + (if needed) `asmcore.fern`.
- The desugar lowers `concurrent`/`spawn`/`await` into the state
  machine: split each task body at its await points into
  continuations (nested functions capturing live locals), seed the
  scheduler, join on `await`.
- **Tests:** parser test (desugar shape), checker test (escape /
  type rules), e2e on all backends (two-task fan-out at the surface
  level produces the same result as the hand-written Phase-0 spec).

The hardest engineering in this phase is **splitting a task body at
await points into continuations** when awaits sit inside arbitrary
control flow (loops, conditionals). Start restricted: awaits at
statement position in straight-line and simple-branch bodies (covers
fan-out); generalize to awaits-in-loops incrementally, each with its
own test.

**Phase 3a — DONE (the fan-out surface, with inline `await`):**
`concurrent { var a = spawn f(args); … return combine(await a,
await b); }` is implemented as a parser-time desugar in the Go
frontend (`internal/lexer` keywords `concurrent`/`spawn`/`await`;
`parser.parseConcurrent`, dispatched inline from `parseBlock`). The
whole block desugars to **one scoped `Block`** (the synthetic
reactor/task/result locals and the join-bound result names stay
confined to the concurrent scope — structured concurrency): a reactor
`var`, one `let (task, rx) = f(rx, args)` Destructure per spawn
(reactor injected as the first arg), a `task.run(...)` call, one
result `var` per binding, then the trailing/join statements. All
spawns start before `run`, so their I/O overlaps. `await a` (handled
in `parseUnary`, gated on an `inConcurrent` depth) is a **join
marker** — `run` has already completed every task before the trailing
statements execute, so it strips to its operand; the keyword
documents the join point and reserves the word for the eventual
suspending form. Spawn targets follow the runtime protocol
`(task.Reactor, args…) -> (task.Step, task.Reactor)`. Verified on
interp / x86-64 / arm64(qemu); compiles on wasm. Tests:
`internal/parser` (`TestParseConcurrentDesugar` + error cases incl.
`await` outside a block) and `examples/tests/async_concurrent_test.fern`
(e2e gate `TestRunnerAsyncConcurrentExamplePasses`). Requires `import
"std/task"`.

**Phase 3b — suspending `await` / the body-splitting CPS transform.** So spawn
targets are written as ordinary functions (no `(Reactor) -> (Step, Reactor)`
protocol leak) and `await` can sit in arbitrary control flow.

- **Slice 1 — DONE (single straight-line await):** an `await` in an ORDINARY
  top-level function body is now a real suspension point (`ast.Await`, produced
  by `parseUnary` for awaits outside the `concurrent` join section). The
  parse-time desugar `desugarTaskFunctionsProgram` rewrites a function shaped
  `{ pre…; var NAME = await EXPR; post…; return E; }` into the std/task protocol
  `(task.Reactor, args…) -> (task.Step, task.Reactor)`: the pre-section + a
  `var (tok, rx2) = rx.register(EXPR)`, a generated `resume(NAME, r)`
  continuation carrying the post-section (each `return E` → `return (Done(E), r)`),
  and `return (Wait(tok, resume), rx2)`. The emitted AST is exactly the
  hand-written CPS form (so it lowers on every backend). Shapes outside slice 1
  (multiple/nested awaits, awaits in control flow, an early `return` before the
  await, awaits in methods/local/generic functions) are REJECTED with a clear
  error, never miscompiled. Tests: `internal/parser` (`TestParseTaskFunctionDesugar`),
  `examples/tests/async_task_fn_test.fern` (e2e gate `TestRunnerAsyncTaskFnExamplePasses`,
  → 3 passes via interp; fan-out + pre/post-await).
- **Slice 2 — DONE (multiple sequential awaits):** the split is now recursive
  (`buildTaskSegment`): each top-level `var NAME = await EXPR;` becomes a
  `register` + a `resume_d(NAME, r_d)` continuation whose body is the next
  segment, so N sequential awaits nest into N continuations (the hand-written
  `start_seq` shape), with per-depth names. The in-scope reactor threads from the
  function's injected `__task_rx` through each `resume`'s reactor param.
  Covered by `start_seq` in `async_task_fn_test.fern` (two awaits → 42) and a
  `TestParseTaskFunctionDesugar` case.
- **Slice 3a — DONE (awaits in terminating conditionals):** a segment whose last
  statement is an `if/else` (both branches present, each ending in `return`) may
  carry `await`s inside its branches. `buildTaskSegment` recurses into each branch
  as its own segment sharing the in-scope reactor — the branches are mutually
  exclusive and nothing follows the `if`, so no merge-point continuation is
  needed. `else if` chains work if they end in a final `else`. Covered by
  `start_branch` in `async_task_fn_test.fern` (then + else paths → 42) and
  `TestParseTaskFunctionDesugar` cases (incl. rejecting code after the if).
- **Slice 3b — DONE (guard-clause await with fall-through):** an await-bearing
  `if` WITHOUT `else` whose then-branch TERMINATES (ends in `return`), with code
  after the `if`. Because the awaiting path returns, the post-`if` code is reached
  only on the `!cond` path — a continuation in the SAME reactor scope, no
  live-state merge — so `buildTaskSegment` recurses the then-branch and the
  fall-through as independent segments. Sound under the `(i32, Reactor)`
  continuation model precisely because no await-bearing path falls through to
  post-`if` code. Covered by `start_guard` in `async_task_fn_test.fern` (taken +
  fall-through paths → 42) and `TestParseTaskFunctionDesugar` cases (incl.
  rejecting fall-through after an await, and a merge after an await-bearing
  `if/else`).
- **Slice 3c (merges) — DONE (no runtime change needed after all):** a NON-terminal
  `if`/`if-else` where an await-bearing branch FALLS THROUGH to shared post-`if`
  code that uses MUTATED state. The earlier worry — that this needs the `(i32,
  Reactor)` continuation to carry arbitrary live state — was avoided by a simpler
  insight: PUSH the post-`if` continuation (REST) into each branch (a deep clone
  per branch), so every branch becomes terminating and reduces to the unified
  conditional lowering. Each path's mutation and its use of REST then live in the
  SAME resume continuation, so value-capture is correct; the branches are
  mutually exclusive, so REST runs exactly once at runtime. `buildTaskSegment` now
  handles every conditional shape (3a/3b/3c) with one rule, and `blockTerminates`
  decides which branches need REST appended. Cost: REST is duplicated per branch
  (fine in practice; deep nesting is the caveat). Covered by `start_merge` in
  `async_task_fn_test.fern` (taken + skipped → 42) and `TestParseTaskFunctionDesugar`.
- **Slice loops-1 — DONE (awaits in a straight-line `while`):** an await-bearing
  `while (cond) { body } rest` lowers to a RECURSIVE loop function
  (`buildTaskWhile` + `buildLoopBody`):
  ```
  function __task_loop_d(carried…, lr): (Step, Reactor) {
      if (cond) { <body, each path ending in `return __task_loop_d(carried…, r)`> }
      <rest as a segment in lr>          // exit, reached when !cond
  }
  return __task_loop_d(<carried initial>…, rxName);
  ```
  The carried set is the in-scope data vars referenced by cond/body/rest, threaded
  as loop params (typed i32 — the task runtime is i32-throughout) so loop-carried
  MUTATION survives across iterations (closure value-capture alone would freeze
  it); the body's end-of-iteration is a raw tail-call to the loop, not a `Done`.
  `buildTaskSegment` now threads a `scope` so the carried set can be computed.
  Covered by `start_sum` in `async_task_fn_test.fern` (accumulate-with-await → 42,
  plus a zero-iteration case) and `TestParseTaskFunctionDesugar`.
  Restricted to a STRAIGHT-LINE body (top-level `await` bindings; no nested control
  flow, `break`, `continue`, or `return`); a labeled loop is rejected too.
- **Slice loops-2 — DONE (range / C-style `for` loops):** an await-bearing
  C-style `for` (which `for i in 0..n` range loops desugar to at parse) is
  rewritten to `init; while (cond) { body; step }` (`rewriteForToWhile`) and
  reuses the while lowering; `init` becomes a lead decl (carried), `step` is
  appended to the body. `buildTaskSegment` also flattens an await-bearing top-level
  `Block` first (range loops parse to a `Block` wrapping `[var __hi, for]`).
  Covered by `start_range` in `async_task_fn_test.fern` and `TestParseTaskFunctionDesugar`.
- **Slice loops-3 — DONE (array `for x in xs` loops):** an await-bearing array
  for-in (still an `ast.ForEach` at task-transform time) is lowered to its
  `.len()`+index `for` form via `ast.DesugarForEachArray`, then flattened and
  run through the for→while path. The carried-var computation was sharpened to
  thread only MUTATED in-scope vars (`mutatedInLoop`) — so the iterated array and
  `len` (immutable) are CAPTURED by the recursive loop closure rather than passed
  as params, which also keeps every loop param an i32 (indices / accumulators).
  Covered by `start_arrsum` in `async_task_fn_test.fern` and `TestParseTaskFunctionDesugar`.
- **Expression-position awaits — DONE:** an `await` in expression position
  (`acc = acc + await x`, `f(await a)`, `return g(await b)`, `if (await c)`) is
  hoisted to a preceding `var __await_h_N = await …;` binding by a pre-pass
  (`hoistTaskExprAwaits` / `rewriteAwaitExpr`) that runs before the CPS transform,
  left-to-right (preserving suspension order). It recurses into nested statement
  bodies but not loop CONDITIONS (re-evaluated per iteration) or nested
  functions / lambdas. So the transform only ever sees binding-form awaits.
  Covered by `start_expr` in `async_task_fn_test.fern` and `TestParseTaskFunctionDesugar`.
- **Loop-body control flow — DONE (`break` / `continue` / nested `if`s):** the
  loop's EXIT (code after the loop) is factored into its own function
  `__task_exit_d(carried…, r)` so the `!cond` path AND any `break` can both jump
  to it; `continue` tail-calls the loop; `return E` is `Done(E)`. `buildLoopBody`
  now lowers nested `if`s in the loop body via the same push-rest-into-branches
  merge machinery and maps the terminators with `wrapLoopReturns`
  (`blockTerminates` counts `break`/`continue`). Labeled break/continue, nested
  await-loops, and await in a match/switch inside a loop are rejected. Covered by
  `start_bc` in `async_task_fn_test.fern` and `TestParseTaskFunctionDesugar`.
- **`await` in a loop CONDITION — DONE:** `while (C) { B }` where `C` contains an
  `await` is rewritten (in `hoistTaskExprAwaits`) to `while (true) { if (!C) {
  break; } B }`, turning the per-iteration condition await into an ordinary in-body
  `if`-condition await (hoisted) + a `break` — both already supported. Covered by
  `start_cond` in `async_task_fn_test.fern` and `TestParseTaskFunctionDesugar`.
- **Early return before an await — DONE:** a guard like `if (bad) { return e; }
  var x = await …;` is routed through the conditional-merge (`lowerTaskIfMerge`):
  the merge now triggers not only on an await-bearing `if` but on any `if` whose
  branch TERMINATES (return/break/continue) with await-bearing code after it, so
  the returning branch terminates and the await code flows to the other branch.
  Covered by `start_guarded` in `async_task_fn_test.fern` and `TestParseTaskFunctionDesugar`.
- **Remaining:** nested await-bearing loops, `await` in a `for`/range loop
  CONDITION (only `while`-cond handled), non-i32 carried/await types, and pairing
  with Phase 1/4 so awaited calls do real I/O rather than the in-memory reactor's
  `register(value)`.
- **Self-hosted parser port** (`examples/self_host/parser.fern`) — the
  desugar must be mirrored there before the Go compiler retires.
  Deferred while the self-host SSA-by-default migration is in flight
  (it sits near that work).
- **Formatter round-trip**: like `use` / `for..in`, the parse-time
  desugar means `fern -fmt` prints the expanded form, not
  `concurrent { … }`. Acceptable (consistent with the existing
  parse-time desugars); a faithful round-trip would need a retained
  AST node + checker/printer support.

### Phase 4 — the first real awaitable: `plat.fetch`

Make the concurrency useful: outbound HTTP. Promote the `Platform`
capability bag (`PLATFORM-RESEARCH.md` Rec §1) so
`plat.fetch(req) : Task[HttpResponse]`, implemented over the Phase-1
reactor (non-blocking connect/send/recv on native; `wasi:http`
outgoing-handler on wasm). Now a handler can issue two fetches and
await both — the trigger condition that motivated the whole design
(`CONCURRENCY-RESEARCH.md`).

### Phase 5 — composition: `select`, cancellation, timeouts

- `select` / happy-eyeballs (first task to finish wins, the rest
  cancel) — **DONE in the runtime** (`task.select`): runs tasks until
  the first reaches `Done`, returns `(winnerIndex, result)`, and
  abandons the losers. Verified (shallow-vs-deep race + already-done)
  on interp / x86-64 / arm64(qemu); wasm compiles. The `select`
  *surface syntax* still waits on Phase 3.
- Scope-bounded **cancellation** — landed structurally with `select`:
  a losing task is simply never resumed and its parked continuation
  (and captured frame) is dropped, which RC/Perceus reclaims (Koka
  `cancelable` shape; one-shot, no `dup`). The Phase-1 reactor will
  additionally close the loser's fd to stop the in-flight OS I/O.
- Timeouts (a timer pollable in the reactor) — still pending; needs
  the Phase-1 real reactor.

> Built early (ahead of Phase 3/4) because it is pure-Fern, fully
> testable, and non-colliding with the in-flight self-host SSA work —
> the same "validate the model in pure Fern first" strategy used for
> the Phase-0/2 core. The hard surface + real-I/O phases (3, 4) and
> the backend reactor (Phase 1) are deferred until the SSA-by-default
> migration lands, so the poll primitive targets the settled path.

## Cross-cutting concerns

- **RC / Perceus.** Continuations are ordinary reference-counted
  heap closures; a completed or cancelled task's continuation is
  dropped (RC reclaims it), so the model needs no special collector.
  Async uses only **one-shot** resumptions (each continuation runs
  exactly once), which is the cheap case — no continuation `dup`
  (the expensive multi-shot path Koka pays for and async never
  needs). Confirm `insert_resource_drops` treats parked
  continuations as held resources.
- **Per-request arena.** Preserve `tcp_serve`'s per-request
  reclamation. The scheduler's allocations (ready-queue, waiter map,
  continuations) live within the request and drop at its end.
- **Memory model.** Single-threaded interleaving within a handler:
  two `concurrent` tasks share locals freely because they never
  execute simultaneously — only their suspension points interleave.
  No cross-task data races, no atomics. (Matches
  `CONCURRENCY-RESEARCH.md` Rec §9; and dodges Lean 4's mark-mt
  graph-walk cost — Fern data stays single-threaded by
  construction.) Parallelism (multi-core) is a separate, later,
  host-scheduler concern, never baked into the language surface.
- **Two frontends.** Until the Go compiler retires, every syntax
  change lands in both `internal/{lexer,parser,ast,checker}` and
  `examples/self_host/{lexer,parser}.fern` (+ `asmcore.fern` for a
  shared checker rule). The desugar output is plain AST, so codegen
  is untouched in both.

## Open decisions (revisit with the user when reached)

1. **Surface keywords.** `concurrent`/`spawn`/`await` vs a block
   form like `parallel { … }` + `.value` joins (the
   `CONCURRENCY-RESEARCH.md` Rec §1 sketch used `concurrent { … }` +
   `select` + `.value`). Naming/ergonomics fork — decide at Phase 3.
2. **`Task[T]` genericity timing.** Tie Phase 2's generic runtime to
   self-host monomorphization, or ship an i32/pointer-concrete
   runtime first and generalize later? (Leaning: concrete first; it
   keeps the self-hosted path green and is what Phase 0 already
   does.)
3. **Awaits-in-loops** in the Phase-3 desugar — restrict initially
   (straight-line + simple branches) and generalize, or build the
   general CFG-split up front? (Leaning: restrict, since fan-out
   doesn't need loop-awaits.)
