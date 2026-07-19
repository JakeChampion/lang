# Concurrency model research

> Correction (2026-07-19): the "no concurrency surface today" framing below predates `std/async` — the colorless `gather`/`race`/`with_deadline` combinators have since shipped (`docs/ASYNC.md`); for the parallelism (multicore) story see `docs/MULTICORE-RESEARCH.md`.

The codebase has *no concurrency surface today* — handlers run
single-shot, one invocation per arena, no `spawn`, no `await`,
no channels, no shared state. That's the right floor for the
edge-handler positioning: cold-start budgets don't tolerate
green-thread runtimes; arena-per-request neatly isolates
state.

This doc is about **the first concurrency primitive the
language picks up**, when one becomes necessary. The
trigger condition is concrete: the first handler that wants
to **issue two upstream fetches in parallel and wait for
both** before responding. That pattern shows up the moment
real edge code exists — fan-out to an auth service + a
config service, fan-out to a cache + a primary store with
the slower one as fallback ("happy eyeballs"), etc.

That single requirement turns out to be the deciding force
on the whole concurrency design. The full menu — actors,
async/await, fibers, virtual threads, channels, futures,
algebraic effects — collapses into two shapes once you
specify "AOT-compiled, cold-start-critical, single-instance-
per-invocation, must support parallel fan-out without
function coloring."

This doc surveys the menu (Erlang, Go, Rust, Pony, Java
virtual threads, Kotlin coroutines, Trio's structured
concurrency, Zig's revisited async, OCaml 5 effects, Inko,
Verona) and lands on a recommendation. Companion to
`PLATFORM-RESEARCH.md` (which already touched on `Task` as
the effect carrier) and `STDLIB-DESIGN-RESEARCH.md` (which
recommended sync-at-language-level for I/O).

## Framing — what concurrency means here

Three orthogonal axes a concurrency design picks values for:

1. **Granularity.** OS threads, virtual threads (fibers),
   futures (stackless coroutines), promises (callback-
   driven), actors. The unit of "a thing that runs."

2. **Communication.** Shared memory + locks, channels (CSP),
   mailboxes (actors), futures (single-value), dataflow
   variables. How tasks pass data.

3. **Composition.** Unstructured `spawn` + `detach` (Go's
   default), structured nurseries (Trio's nursery scope),
   futures join points (Rust's `tokio::join!`),
   supervisor trees (Erlang). How tasks compose.

Each combination has a name in some language. The
interesting question for this codebase isn't *which one
exists*; it's *which combination is cheapest to add to
the current trajectory* without breaking cold-start or
forcing async function coloring on every handler.

The constraints that narrow the design space:

- **AOT compilation.** No JIT-warmup-amortises-everything
  bet. Runtime concurrency machinery pays at cold-start.
- **WASI Preview 2's `pollable` is the underlying I/O
  primitive.** Whatever concurrency model we pick has to
  reduce to "wait for any of N pollables to be ready" on
  WASI.
- **Per-request arena.** Concurrent tasks within a single
  request *share the same arena*. Cross-request sharing
  is not a thing.
- **Single user, breaking changes free.** No legacy.
- **No function coloring** (Bob Nystrom's "What Color is
  Your Function" — explicitly cited in
  `LANGUAGE-DIRECTION.md ▸ Anti-patterns`).

## What we already do well — call out so we don't drift

- **Sync-at-language-level posture stated.** Per
  `STDLIB-DESIGN-RESEARCH.md ▸ I/O ▸ Sync vs async`: WASI
  Preview 2's poll-shaped streams hide under a sync
  `Reader` / `Writer` wrapper. Right call.
- **No `async`/`await` in the surface.** Yet. The Kyo /
  Koka effect-row exploration in
  `LANGUAGE-DIRECTION.md ▸ Algebraic effects` is the
  forward-compatible alternative.
- **`Result[T, E]` + `?` operator is the canonical error
  shape.** Composable with future-style concurrency
  values (a `Future[Result[T, E]]` collapses to
  `Result[T, E]` after await).
- **Per-request arena is single-threaded by construction.**
  No cross-thread aliasing problems within one request.
  Multi-request parallelism is the *runtime's* concern
  (the host runs multiple WASI instances), not the
  *language's* — handler code stays single-threaded.

## Single-source deep dives

### Go — goroutines + channels (CSP)

Sources:
- "Communicating Sequential Processes" (Hoare 1978).
- Russ Cox, "Go Concurrency Patterns" + "Bell Labs and
  CSP Threads."
- `runtime/proc.go` in the Go source.

**The headline design:**

- `go f(x)` spawns a goroutine — a function call on a
  stack independent of the caller's. Lightweight (~2 KB
  initial stack), grown on demand.
- Goroutines are scheduled cooperatively by Go's runtime
  onto M:N OS threads.
- Communication via *channels*: typed FIFO queues with
  optional buffering. `ch <- x` sends; `<-ch` receives.
  Blocking operations.
- `select` lets a goroutine wait on multiple channel
  operations at once.

**What Go gets right:**

- **No function coloring.** Any function can spawn a
  goroutine; any function can use channels. There's no
  "async fn" vs "sync fn" distinction. Code reads
  straight-line.
- **CSP composes.** Two channels feeding one goroutine
  that fans out to four others is straightforwardly
  expressible.
- **Cheap goroutines.** 100k goroutines is fine.
  Goroutines are not OS threads.
- **`select` for multi-source wait.** Universal pattern.

**What Go gets wrong:**

- **Spawn-and-detach is the default.** `go f(x)` and
  forget. If `f` panics, the runtime crashes. If `f`
  hangs, you can't cancel it from outside. Goroutine
  leaks are a real production problem.
- **No structured concurrency.** Cancellation propagates
  through `context.Context` *if* you remember to thread
  it; many libraries don't.
- **Shared mutable state without `Send`/`Sync` discipline.**
  Race conditions are runtime bugs, caught by the race
  detector (a build-time opt-in) or not at all.
- **2 KB initial stack** still costs something. For a
  100-goroutine handler that runs millions of times,
  the per-goroutine memory adds up.

**What translates:**

- **No function coloring** is the right posture. Any
  function can spawn; any function can wait.
- **CSP-shaped channels** are a sound communication
  primitive *if* we adopt them with structured
  concurrency (next section).
- **`select`-shape multi-source wait** is the right
  primitive for "wait for any of these" patterns.

**Considered, left:**

- **Goroutines themselves.** Stackful coroutines need
  per-goroutine stack memory + a scheduler. Cold-start
  hostile *if* the runtime initialises N goroutine
  pools at startup. Right answer for long-lived servers,
  wrong for AOT cold-start.
- **`go f(x)` spawn-and-detach.** Without structured
  cancellation, leaks. Trio's nurseries (next) fix this.

### Trio — structured concurrency

Sources:
- Nathaniel J. Smith, "Notes on structured concurrency,
  or: Go statement considered harmful" (2018).
- https://trio.readthedocs.io/
- The Curio + AnyIO + Kotlin coroutines family that
  followed.

**Trio's core idea: every task lives within a *nursery*
that owns it.** No spawn-and-forget. The nursery scope
syntactically delimits the lifetime of every task spawned
inside it:

```python
async with trio.open_nursery() as nursery:
    nursery.start_soon(fetch, "https://auth")
    nursery.start_soon(fetch, "https://config")
# When this line is reached, BOTH fetches have completed
# (or all have been cancelled if either raised).
```

The block doesn't exit until *every* task it spawned
completes. If any task raises, *all* are cancelled.
Errors propagate as exceptions out of the nursery's
exit.

**What structured concurrency gets right:**

- **Tasks have a scope, just like values.** A nursery
  is to a task what a function frame is to a local
  variable. Scope-exit cleans up.
- **Cancellation is by scope.** Cancel the nursery and
  every task inside it cancels. Cancel propagation is
  syntactic, not "if you remember to thread the
  context."
- **Errors don't disappear.** A failed task's exception
  re-surfaces at the nursery's exit. No "fire-and-forget
  task swallowed a 500 error."
- **Composes recursively.** A nursery can be created
  inside another task's body. The outer nursery doesn't
  exit until inner ones do.

**What translates:**

- **Adopt the nursery shape.** The closest fit for arena-
  per-request: a *scope* in Fern has nursery semantics
  by default. Spawning a task means spawning within the
  current scope; scope-exit waits for all tasks; error
  in any cancels the rest.

  Sketch:

  ```
  function handle(req: HttpRequest, plat: Platform): HttpResponse {
      var auth_task: Task[AuthResp];
      var config_task: Task[ConfigResp];

      concurrently {
          auth_task   = plat.fetch(auth_req);
          config_task = plat.fetch(config_req);
      }
      // Both tasks complete (or have cancelled the other).
      var auth = auth_task.value?;
      var config = config_task.value?;

      return build_response(auth, config);
  }
  ```

  Or with sugar:

  ```
  var (auth, config) = concurrent (
      plat.fetch(auth_req),
      plat.fetch(config_req),
  )?;
  ```

  The `concurrent` keyword opens a nursery scope; the
  block waits for all listed tasks; results are returned
  as a tuple of `Result`s.

- **Cancellation = nursery exit.** Aligns with arena-on-
  exit-resets-everything. If one fetch fails, the other's
  in-flight pollable is cancelled, the partial state
  rolls back with the arena.

### Erlang / OTP — actors and supervisor trees

Sources:
- Joe Armstrong, "Making reliable distributed systems in
  the presence of software errors" (PhD thesis 2003).
- Erlang's process model: `spawn`, `send`, `receive`.

**The headline design:**

- Every concurrent thing is a *process* — an
  independently-heap-allocated unit with its own mailbox.
- Processes communicate by sending messages to each
  other's mailboxes (no shared memory).
- `receive` blocks until a matching message arrives.
- *Supervisor trees* arrange processes into a hierarchy:
  if a child crashes, the supervisor decides whether to
  restart, escalate, or terminate.
- "Let it crash" — processes are cheap to recreate; don't
  defensive-program error recovery in every function.

**Why Erlang's model is so robust:**

- **No shared mutable state across processes.** Data
  races impossible by construction.
- **Failure isolation.** One process crashing doesn't
  take down others; supervisor decides the recovery
  strategy.
- **Hot code swap.** Production code can be replaced
  without downtime. Each process has a current version;
  new processes use the new code.

**Why Erlang doesn't fit cold-start edge handlers:**

- **Heavy runtime.** The BEAM VM is a fully-featured
  language runtime with GC, scheduler, mailbox
  infrastructure. ~MB of cold-start memory and ~10 ms
  warmup just for the VM. Hostile to cold-start.
- **Designed for *long-lived processes*.** Phone-switch-
  uptime is the motivating workload. Edge handlers are
  the opposite — millisecond duration, ephemeral.

**What translates:**

- **Process isolation as a *design principle*** — already
  aligned with per-request arenas. Each handler invocation
  is its own "process" in the Erlang sense, except it's
  scheduled by the WASI host rather than by an in-process
  VM.
- **Supervisor-tree-shaped error handling** — once
  structured concurrency exists (Trio section), the
  nursery is a degenerate supervisor: it catches errors
  from its children and decides what to do.

**Considered, left:**

- **The actor model as the language's primary concurrency
  surface.** Wrong shape for AOT-cold-start. Right for
  long-lived servers, where Pony / Inko / Akka shine.
  Edge handlers don't need it.

### Rust async + Tokio — function coloring, sound but
expensive in surface area

Sources:
- https://rust-lang.github.io/async-book/
- https://tokio.rs/
- Bob Nystrom, "What Color is Your Function?" (2015).
- Niko Matsakis's blog series on Rust async internals.

**The headline design:**

- `async fn f() -> T` returns a `Future<Output = T>`. A
  Future is a state machine; `f()` doesn't run yet, it
  returns the state machine.
- `.await` polls a Future to completion. Only callable
  inside an `async fn`.
- A runtime (Tokio, async-std, smol) drives the futures
  to completion by polling them and waiting on I/O.
- Send / Sync marker traits prevent data races across
  await points.

**What Rust async gets right:**

- **Zero-cost abstractions.** A future compiles to a
  state machine of unboxed unions. No allocation per
  task unless explicitly boxed.
- **Type-checked race freedom.** The borrow checker
  + Send / Sync ensures no shared mutable state across
  threads / await points.
- **`tokio::join!` for structured parallel waits.** All-
  succeed or all-cancel semantics.
- **Backpressure via bounded channels.** `tokio::sync::
  mpsc` with bounded capacity blocks the sender when
  full.

**What Rust async gets wrong (or what its critics cite):**

- **Function coloring.** `async fn` and `fn` have
  different types. Calling an `async fn` from a `fn`
  requires runtime ceremony (`block_on`). Two parallel
  function-call hierarchies, the colour propagates.
- **`Pin` and self-referential futures.** The borrow
  checker's interaction with future state machines
  spawned an entire sub-language (`Pin<&mut Self>`)
  that confuses everyone.
- **Cancellation is cooperative.** Drop the future, the
  state machine stops being polled. But the *operation*
  the future was driving (an in-flight HTTP request)
  doesn't necessarily get cancelled at the OS level.
- **`Send` everywhere.** Every future captured into a
  spawn needs to be `Send`; the inference around this
  is one of Rust's biggest "why is the compiler yelling
  at me" sources.

**What translates:**

- *Don't* take Rust async's surface. Function coloring
  is the explicit anti-pattern. The state machine
  *encoding* is fine (it's what the AOT compiler
  produces anyway); the *user-facing* shape isn't.

- **Backpressure via bounded channels** is the right
  shape *when* channels enter the picture. Defer until
  needed.

**Considered, left:**

- *async/await as the surface*. No. (Doubled down on
  in the Anti-patterns section.)
- *Send / Sync marker traits*. Need a real concurrency
  story before they're applicable. With single-arena-
  per-request, cross-thread data races aren't possible
  by construction.

### Java virtual threads (Project Loom)

Sources:
- JEP 444 (Java 21 stable virtual threads).
- Ron Pressler, "Why Virtual Threads?" presentations.

**The headline design:**

- Same `Thread` API everyone already knows.
- A "virtual thread" is scheduled by the JVM, not the OS.
  Costs ~KB of memory, not MB.
- `Thread.startVirtualThread(() -> work())` spawns one.
- Blocking I/O *just works* — the virtual thread
  yields its carrier OS thread, gets re-scheduled when
  ready.
- **No `async`/`await`. Same code reads in synchronous
  shape.**

**What virtual threads get right:**

- **No function coloring.** The killer feature; Java
  shipped it after a 6-year project (Loom) explicitly to
  un-do the async-stack-trace mess.
- **Massive concurrency.** 1M virtual threads per JVM
  fine. Scales past Go-goroutine numbers.
- **Existing code works.** Blocking calls are virtual-
  thread-aware via the JVM's I/O subsystem rewiring.

**What virtual threads don't fit:**

- **Requires a runtime that can re-schedule threads.**
  The JVM does this; AOT-compiled Fern would need to
  build a fibres-on-top-of-WASI scheduler. Doable but
  non-trivial.
- **Cold-start.** JVM startup is huge. Not a problem for
  Fern (we're AOT) — but the scheduler we'd build pays
  cold-start tax.

**What translates:**

- **No-function-coloring posture.** The right surface
  shape *if* we eventually have parallelism. Whether
  we implement it via virtual threads or via futures
  is the orthogonal axis; the surface is the same.

- **"Existing code works."** Adding concurrency
  shouldn't make every existing function add an `async`
  keyword. Pile a `concurrent` block on top; don't
  refactor every function below it.

**Considered, left:**

- *Building a virtual-thread scheduler from scratch.*
  Multi-month effort. Skip; do simpler designs first.

### Pony — reference capabilities for race freedom

Sources:
- https://www.ponylang.io/
- Sylvan Clebsch's PhD thesis on Pony.
- Inko (descended from Pony) — https://inko-lang.org/

**The headline design:**

- Every value has a *reference capability*: `iso`
  (uniquely owned), `val` (immutable), `ref` (mutable
  but not shareable), `box` (read-only), `tag`
  (opaque), `trn` (transitionable).
- The compiler enforces that only safely-shareable
  capabilities cross actor boundaries.
- Actors communicate via async messages; the type
  system proves no data races.

**Why Pony's design is brilliant:**

- **Provable race freedom at compile time.** Not
  "we trust the runtime"; not "the race detector
  caught it"; *the program with a race won't type-check*.
- **No locks.** Sharing is restricted to capabilities
  that don't admit races.

**Why Pony's design doesn't fit cold-start edge handlers:**

- **Heavy type-system extension.** Capability annotations
  on every reference. Trade-off pays off for long-lived
  multi-actor systems; doesn't for handler-per-request.
- **Actor scheduler at runtime.** Cold-start cost.

**What translates:**

- **The *principle* — make races impossible by type,
  not by runtime check — is right.** For our shape:
  single-threaded handler + arena → no races by
  construction, *without* capability annotations.
  When/if cross-task sharing enters (within one
  handler scope), Pony's capabilities are the
  reference for how to design the type discipline.

**Considered, left:**

- *Reference capability annotations everywhere.*
  Overkill for the per-request-arena case. Revisit
  if/when multi-actor edge handlers become a thing.

### Kotlin coroutines — structured async without the worst Rust pain

Sources:
- https://kotlinlang.org/docs/coroutines-overview.html
- Roman Elizarov's talks (Kotlin's coroutines lead).

**Kotlin's design: stackless coroutines + structured
concurrency. `suspend` is the keyword (Kotlin's
function-colour); `coroutineScope { … }` is the
nursery.**

- `suspend fun f()` is a function that *can* suspend.
- Inside `coroutineScope { … }`, `launch { … }` spawns;
  the scope waits for all launches.
- Cancellation is *cooperative* but the language ensures
  every suspension point checks for cancellation.

**What Kotlin gets right vs Rust async:**

- **No `Pin` / self-referential-future gymnastics.**
  Kotlin's stackless coroutines are simpler.
- **Structured by default.** `launch` outside a
  `CoroutineScope` is impossible.
- **Stack traces work.** Suspended coroutines have
  reconstructable async stack traces.

**What Kotlin still gets wrong:**

- **Still has function coloring.** `suspend fun` ≠
  `fun`. Calling `suspend fun` from `fun` requires
  `runBlocking { … }`.

**What translates:**

- **Structured-concurrency-by-construction.** Same
  takeaway as Trio. The nursery shape composes with
  arena-scoped lifetimes.

- **Sane stack traces across suspension points.** Once
  the language has any form of suspension, error
  propagation should preserve the conceptual call
  chain.

### Zig's revisited async (deprecated + reconsidered)

Source:
- Andrew Kelley, "Goodbye to the @Async" (2024
  announcement).
- Zig issue #6025 (the async removal RFC).

**Zig had colorless async in 2019-2023. It was removed
in 2024.** Andrew Kelley's reasoning:

- **The "colorless" claim wasn't quite true.** Async
  functions in Zig still needed a frame allocation at
  the call site; this complicated calling from arbitrary
  contexts.
- **The implementation interacted poorly with multiple
  backends.** Cold-start performance and code-gen
  complexity both regressed.
- **Most Zig programs don't need it.** The std lib's
  I/O is sync by default; users who want async build
  it on top.

**The replacement plan**: build async at the library
level via event loops + explicit state machines, not as
a language feature.

**What translates:**

- **Zig's experience is informative for us.** Building
  async into the *language* (as a primitive) interacts
  poorly with multi-backend codegen + cold-start. Build
  it as a *library* (a `Task` type + a `concurrent`
  combinator scope) instead.

- **Std-lib I/O stays sync.** Per
  `STDLIB-DESIGN-RESEARCH.md`'s recommendation.
  Concurrent fan-out is a separate composition on top.

### OCaml 5 effects — algebraic effects for concurrency

Sources:
- OCaml 5 release notes.
- KC Sivaramakrishnan et al., "Retrofitting Effect
  Handlers onto OCaml" (PLDI 2021).

**OCaml 5 added algebraic effects + handlers as a
language primitive.** Concurrency is one of several
things you can build on top:

- An effect `Async` is declared.
- A function that uses `Async.suspend` *performs* the
  effect.
- A handler installed at scope entry decides what to
  do when the effect is performed — schedule the
  continuation, fork it off, etc.

This is what Kyo + Koka build on
(`LANGUAGE-DIRECTION.md ▸ Algebraic effects`).

**The advantage**: concurrency is *one library* — a
particular handler — among many. Same primitive
discharges retries, state, async, generators, parser
backtracking, etc.

**The cost**: effects in the language are a non-trivial
type-system extension. The OCaml community is still
figuring out best practices 3 years post-release.

**What translates:**

- **The Kyo/Koka effect-row direction the codebase is
  already considering** unifies concurrency, errors,
  state into a single mechanism. If/when effects land,
  the `Task` concurrency primitive is one effect among
  many.

- **But concurrency doesn't have to *wait* for effects.**
  A direct `Task` + `concurrent {}` primitive is
  cheaper and ships independently. Effects later
  subsume it.

### Verona — concurrent ownership at scale

Source:
- https://github.com/microsoft/verona
- Sylvan Clebsch et al.'s Verona papers (Microsoft
  Research).

**Verona is a research language whose design extends
Pony's reference-capability idea with explicit *region*
ownership and concurrent ownership.** Most relevant
finding: regions with isolated state can be passed
between concurrent contexts safely if the type system
tracks ownership.

This matches the codebase's per-request arena story:
each handler invocation owns its arena (a region);
crossing tasks within one arena is fine because they
share the arena's ownership.

**What translates:**

- **Arena-as-region maps cleanly to "tasks share the
  arena."** Within one handler invocation, parallel
  tasks share the arena and can read/write freely
  because the *whole handler* completes before the
  arena resets. No use-after-free, no data race risk
  with single-threaded scheduling.

- **The verification story stays simple.** Single-
  threaded fibres-on-poll means no actual parallelism
  *within* a handler; concurrency is just interleaving.
  Two fetches "in parallel" are really two pollables
  waiting on different sockets; one returns first, its
  task progresses, the other waits.

## Cross-cutting themes

1. **No function coloring is the modern convergence.**
   Java's Loom, Kotlin's suspend (controversially),
   Go's goroutines, Zig's removed-and-coming-back
   async — every recent design avoids two-coloured
   function spaces.

2. **Structured concurrency.** Trio's nurseries,
   Kotlin's `coroutineScope`, Java's `StructuredTask
   Scope`. Unstructured spawn-and-detach is a known
   wart (Go); modern designs require lexical
   scope-bounded task lifetimes.

3. **Single-threaded interleaving covers most use cases.**
   Real parallelism (multi-core) is the bonus, not the
   primary case. WASI handlers run in a single thread;
   even Go's parallel-fetch is mostly waiting on I/O.

4. **Build concurrency as a library where possible.**
   Zig's experience. Language primitives for
   concurrency interact with codegen + cold-start;
   library primitives stay isolated.

5. **Cancellation is by scope, not by token.** Trio,
   Kotlin, Java. Per
   `STDLIB-DESIGN-RESEARCH.md ▸ I/O ▸ Error model`'s
   `Result[T, IoError]` posture: nursery-exit on error
   propagates cancellation outward; the arena's
   cleanup-on-reset closes any remaining I/O handles.

6. **Per-request arena is already the right
   isolation boundary.** Erlang's process-per-task,
   Pony's actor-per-task, our arena-per-request — same
   idea at different scale. Within one arena, single-
   threaded interleaving is sound; across arenas, no
   sharing is the right default.

7. **Backpressure matters when streams enter.** Bounded
   channels (Rust Tokio's `mpsc` with capacity). Defer
   until streams exist.

## Concrete recommendations

Ordered by leverage × cost. Several depend on the
`Platform` parameter from `PLATFORM-RESEARCH.md ▸ Rec §1`.

### 1. Adopt structured concurrency as the *primary* shape

**Cost: 3-4 weeks (language design + checker + runtime).**
**Impact: gates the parallel-fetch use case; defines the
posture forever.**

`concurrent { … }` block; all tasks spawned inside complete
(or all-cancel) at the closing brace. Sketch:

```
function handle(req: HttpRequest, plat: Platform): HttpResponse {
    var auth: Result[AuthResp, FetchError];
    var config: Result[ConfigResp, FetchError];

    concurrent {
        auth   = plat.fetch(auth_req);
        config = plat.fetch(config_req);
    }
    // At this point, both fetches have completed (or both
    // have been cancelled because one of them raised).

    return build_response(auth?, config?);
}
```

Equivalent sugar:

```
var (auth, config) = concurrent (
    plat.fetch(auth_req),
    plat.fetch(config_req),
);
```

Semantics:

- All listed expressions are scheduled concurrently.
- If any expression raises, the others are cancelled.
- The block exits after all complete.
- Each expression's result is bound in the surrounding
  scope (or returned as a tuple in the sugar form).

The `concurrent` keyword is a *scope*, exactly like
`for` or `if`. No function coloring; any function
containing one is still a regular function.

### 2. No function coloring; sync surface stays sync

**Cost: 0 (a design decision).** **Impact: critical.**

Tasks that suspend (waiting on a pollable) do *not*
turn the containing function into an `async fn`. The
language stays single-shape.

How: the compiler infers per-function "may suspend"
status from the call graph. Suspension points are
calls to runtime intrinsics (`plat.fetch`, `Stream.read`,
`Time.sleep`); functions that transitively reach them
get a hidden flag set in the IR.

In the surface, `function f()` is unchanged. The
runtime knows which functions may suspend; the user
doesn't have to.

Mirrors Java Loom's posture exactly.

### 3. Cancellation = scope exit on error

**Cost: 1 week (after §1).** **Impact: gates correctness
when fan-out partially fails.**

If one task inside a `concurrent { … }` block raises,
the others are *cancelled*: their pollables are
unregistered from the runtime, their in-flight WASI
operations are aborted (where the host supports it).

The arena's cleanup at handler-exit ensures any
partial state is freed.

No `try / catch / cancel-explicitly` ceremony.
Structural by construction.

### 4. `Task[T]` as the unit, but unboxed where possible

**Cost: 2 weeks.** **Impact: composition story.**

A `Task[T]` is a future-like value: an in-flight
computation that will eventually produce a `T`. Tasks
can be:

- Spawned inside `concurrent { … }`.
- Joined with `.value` (blocks until ready).
- Combined via `select` (wait for any of N tasks).

Unboxed where the compiler can statically determine
the task's shape (most common case: a single-fetch
task). Boxed when the task escapes its creation scope
(rare in handler code).

### 5. `select` for "any-of-N" wait

**Cost: 1 week.** **Impact: medium; gates happy-eyeballs
patterns.**

```
var winner = select (
    task primary   { … plat.fetch(primary_req)   … },
    task fallback  { … plat.fetch(fallback_req)  … },
);
```

The first task to complete wins; the rest cancel. Same
shape as Go's `select`, structured-concurrency-aware.

### 6. `Channel[T]` as a separate, deferred primitive

**Cost: 3-4 weeks; defer until streams need it.**
**Impact: medium; not yet needed.**

Bounded MPSC (multi-producer single-consumer) channel
for streaming patterns. Producer / consumer task in
the same `concurrent { … }` scope; producer pushes
into the channel, consumer pulls.

Used for server-sent-events fan-in, streaming-body
processing, etc. Don't ship until a real handler wants
it. Bounded capacity provides backpressure.

### 7. WASI poll-based runtime under the surface

**Cost: 2-3 weeks.** **Impact: needed for §1 to actually
run.**

The Fern runtime maintains a `Poller` that owns
pending pollables (one per task waiting on I/O). On
each scheduler tick:

- Call `wasi:io/poll/poll(pollables)` to wait until
  at least one is ready.
- Wake the corresponding task.

Single-threaded scheduler. ~50 LOC of glue. Lives in
the platform-glue layer (per
`PLATFORM-RESEARCH.md ▸ Rec §2`), not in the
compiler.

For native arm64 / x86_64 targets, the analogous
mechanism is `epoll` (Linux) / `kqueue` (Darwin).

### 8. Defer actors, refcaps, full effect system

**Cost: 0 (a deferral).** **Impact: avoids over-
engineering.**

Specifically *do not* take on:

- Erlang-shape actor model. Wrong scale for edge
  handlers.
- Pony-shape reference capabilities. Wrong cost-
  benefit for arena-scoped handlers.
- OCaml 5-shape algebraic effects *as the way to
  reach concurrency*. Effects subsume concurrency
  later, after `concurrent { … }` is the working
  surface.

### 9. Memory model: sequential within a handler, no
sharing across

**Cost: 0 (a constraint that falls out).** **Impact:
high — makes everything else simpler.**

Within one handler invocation: single-threaded
interleaving, no actual parallelism. Two `concurrent`
tasks read and write the same locals freely because
they never *execute* simultaneously — only their
suspension points interleave.

Across handler invocations: no sharing. Each handler
gets a fresh arena; the host scheduler runs N handlers
on N OS threads; no cross-handler aliasing.

This is the strongest possible memory model and *also*
the cheapest to implement. The cost is "no actual
parallelism within one handler," which is fine for
the edge-handler workload (the I/O is the wait, not
the CPU).

### 10. Document concurrency story in `LANGUAGE-DIRECTION.md`

**Cost: 1 day after §1 lands.** **Impact: prevents drift.**

Once §1 lands, write a short section in
`LANGUAGE-DIRECTION.md ▸ Outside influences we mined`
recording:

- The decision (structured concurrency, no function
  coloring, scope-bounded cancellation).
- What was considered and rejected (actors, refcaps,
  Rust-async, effect-handlers-as-primary).
- The trigger condition for revisiting (parallel
  parallelism > I/O-bound, multi-handler-in-one-binary,
  cross-handler state).

Mirrors the existing TigerStyle / Kyo / influences
patterns.

## Anti-patterns — explicit "do not adopt"

- **`async`/`await` with function coloring.** Bob
  Nystrom's "What Color is Your Function" essay is the
  canonical reference. Splits the stdlib + every user
  function into red and blue worlds. Java Loom + Zig's
  revisit + this codebase's existing stance all agree:
  don't.

- **Unstructured `spawn` + `detach`.** Go's default;
  source of goroutine leaks. Modern designs require
  task lifetime ≤ enclosing scope.

- **Shared mutable state across tasks** (Go's posture
  without `sync` discipline; pre-Loom Java). Aliasing-
  free single-threaded interleaving avoids the entire
  problem.

- **Actor model as the *primary* concurrency surface.**
  Right answer for long-lived servers (Erlang, Pony).
  Wrong for arena-per-request edge handlers — pays for
  isolation that's already free.

- **Reference capabilities pervasive in the type system**
  (Pony's `iso` / `val` / `ref` / `box` / `tag`). Right
  cost-benefit for multi-actor systems; overhead for
  arena-scoped single-handler code.

- **OS threads as the unit.** Cold-start hostile;
  per-handler is a logical unit, not a thread.

- **Cooperative cancellation via passing a token
  everywhere.** Go's `context.Context`. Mostly works,
  but only if every library threads it; many don't.
  Scope-based cancellation (Trio / Kotlin) is more
  robust.

- **Backpressure-free unbounded channels.** Memory-
  exhaustion failure mode. Bounded by default.

- **Multi-threaded mutation within a handler.** Adds
  the entire data-race-prevention complexity for
  zero benefit (handler is I/O-bound). Single-
  threaded interleaving covers the use case.

- **Concurrency as a language primitive built into
  codegen.** Per Zig's experience: build it as a
  library on top of WASI's poll. Codegen stays simple;
  each backend doesn't need its own scheduler.

## When to revisit

- **When the first real handler wants two parallel
  fetches.** That's the trigger for Rec §1.

- **When a real workload wants server-sent events or
  request body streaming.** Rec §6 (channels) becomes
  the next step.

- **When a single handler invocation legitimately
  benefits from multi-core CPU parallelism** (not just
  I/O overlap). That's the trigger for revisiting
  the "no actual parallelism within one handler"
  constraint. Likely never for edge handlers; possibly
  for CLI tools doing data processing.

- **When the codebase grows enough handlers that
  durable cross-handler state is worth the complexity**
  (a connection pool, a cache, a session store). At
  that point the *runtime* picks up actor-shape
  isolation between long-lived services and short-
  lived handlers; the language surface stays the
  same. The right shape is then "the platform
  exposes long-lived services as bindings" (per
  `PLATFORM-RESEARCH.md ▸ Service bindings`).

The single highest-leverage recommendation is
**Rec §1 (`concurrent { … }` with structured
concurrency)** + **Rec §2 (no function coloring)**.
Together they define the entire concurrency posture
without committing the language to anything heavier.
Everything else is incremental composition on top of
that foundation.
