# Async / concurrency implementation research — Koka, Lean 4, Roc, and the AOT/WASM mechanics

Companion to `CONCURRENCY-RESEARCH.md`. That doc surveyed the
*menu* (Erlang, Go, Rust, Pony, Loom, Kotlin, Trio, Zig, OCaml 5,
Inko, Verona) and landed on a recommendation: **colorless
structured concurrency** (`concurrent { … }` + `select`), a
single-threaded poll-based runtime under the surface, no function
coloring, no actors/refcaps/effect-system-as-primary.

This doc goes one level deeper on a narrower question the user
asked directly: **how do Koka, Lean 4, and Roc actually
*implement* async/concurrency, what do those mechanisms compile
to, and which mechanism is the right *implementation* for Fern's
chosen surface across three AOT backends (arm64, x86-64, wasm32)?**

It is a deeply-sourced, falsifiable-claim-level study. Every
non-obvious claim carries a confidence rating and a primary
source. The headline conclusion **agrees with and sharpens**
`CONCURRENCY-RESEARCH.md`: the right *implementation* of the
colorless surface is a **compiler-side stackless state-machine
(CPS) lowering in the target-agnostic IR, driven by a
single-threaded readiness reactor** — never stackful green
threads (per-arch assembly + cold-start tax) and not yet a
general algebraic-effect system. The three studied languages, read
carefully, all point the same way.

> Sourcing caveat carried from the research pass: many primary
> pages (koka-lang.github.io, lean-lang.org, roc-lang.org, v8.dev,
> emscripten.org, kristoff.it) return HTTP 403 to automated
> fetchers. Claims resting only on search-engine extractions of
> those pages, or on secondary syntheses, are marked accordingly.
> GitHub `/blob/` source, arXiv PDFs, and MSR PDFs were read
> directly and are high-confidence. Quantitative figures
> (switch-time nanoseconds, exact tool versions, ship dates) are
> the least reliable and **do not affect the recommendation**.

---

## TL;DR recommendation

1. **Implement the `concurrent { … }` surface as a stackless CPS
   / state-machine transform in `internal/ir`.** Each suspension
   point (an `await` on a platform I/O op) becomes a state
   transition; the task's live locals become an explicit heap
   record. This is exactly how Rust `async fn` → `Future::poll`
   and C# `async` → state machine work. It lives **once** in the
   target-agnostic IR and every backend inherits it — no per-arch
   work. This is the single most important finding: it is what
   lets one mechanism serve arm64 + x86-64 + wasm32 unchanged.

2. **Drive it from a single-threaded readiness reactor** built on
   `wasi:io/poll.poll` (wasm), `epoll` (Linux), `kqueue`
   (Darwin). No scheduler in codegen; ~50 LOC of platform glue, as
   `CONCURRENCY-RESEARCH.md ▸ Rec §7` already says.

3. **Do NOT use stackful coroutines / green threads.** They
   require hand-written assembly *per target ABI* (Boost.Context-
   style) and a per-task reserved stack — a direct cold-start and
   cross-backend-cost hit. Go pays a mandatory always-linked
   scheduler+GC and a ~1 MB binary floor for the privilege. Wrong
   trade for fast-startup edge handlers.

4. **Do NOT adopt a general algebraic-effect system *yet* — but
   know it is the colorless end-game.** Koka and OCaml 5 prove
   effect handlers give colorless direct-style async that compiles
   to native with no stack switching. If Fern ever wants a general
   effect system, the *same* stackless-CPS machinery from (1) is
   the lowering target. Effects subsume concurrency later; they
   are not the cheapest first step. (Matches
   `CONCURRENCY-RESEARCH.md ▸ Rec §8`.)

5. **The Roc host-split is the relevant precedent for *where the
   event loop lives*, not for the language surface.** Keep the
   reactor in the platform-glue layer (the "host"), keep the
   language surface colorless and pure-ish above it. Fern already
   leans this way (`PLATFORM-RESEARCH.md`).

6. **Edge handlers need *concurrency* (overlap N I/O waits within
   one request), not *parallelism* (multi-core CPU).** That single
   distinction collapses the design: a single-threaded reactor +
   stackless tasks is sufficient and is the cheapest correct
   thing. Parallelism is a separable, later, host-scheduler
   concern.

---

## 1. Koka — algebraic effects, evidence passing, Perceus

**The single most important Koka finding, because it is widely
mis-stated:** Koka has *two* effect-handler implementation
strategies, and the production one does **not** copy or switch
stacks.

- **Default C backend (production path, `koka --backend=c`)**:
  resumptions are captured **monadically by "yield bubbling"**.
  An operation `yield`s; the yield bubbles up to its matching
  prompt (identified by a unique `marker`), and the resumption is
  *reconstructed as a composed function* from the explicit
  continuations appended along the way up. Re-invoking the
  resumption re-enters that function. **No OS-stack manipulation.**
  *Confidence: high.* (`xnning.github.io/papers/multip.pdf`;
  Generalized Evidence Passing, MSR; `std_core_hnd.html`)
- **`libmprompt`/`libmpeff` (experimental, separate library)**:
  uses in-place growable virtual-memory "gstacks" (~4 KiB
  committed → 8 MiB) with address stability and real stack
  switching. **64-bit only; explicitly "should not be used in
  production."** Any web claim that "Koka switches/copies stacks"
  is about *this*, not the default backend. *Confidence: high.*
  (`github.com/koka-lang/libmprompt`)

### How async is modelled

- Koka models `async`/`await` as a **library on algebraic effect
  handlers, with no async/await language keywords.** *High.*
  (`github.com/koka-lang/koka`; structured-asynchrony TR)
- `std/async` (v1): the `async` effect's core op is
  `do-await(setup, scope, cancelable?)`. `await` runs `setup` to
  register a host callback, then *yields* (suspends); when the
  host event loop later fires the callback with a `try<a>`, the
  suspended computation is *resumed*. Setup type:
  `(cb:(try<a>,bool)->io ()) -> io maybe<()->io ()>` — the
  returned `maybe` is an optional cancellation action. *High.*
  (`lib/std/async.kk` @ v1-master; structured-asynchrony TR)
- **Structured concurrency falls out of handlers**: `interleaved`,
  `interleavedx`, `cancelable` scopes, scope-IDs forming
  parent/child relations for nested cancellation. (Some
  `firstof`/`timeout` is commented out in current source.)
  *Medium* (paper describes the full set; source has gaps).
- Resumption discipline is fixed at the *clause* site:
  tail-resumptive (`fun`/`val`, resumes once in place, the
  optimized common case), `control`/`ctl` (captures first-class
  `k`, may resume 0/1/many times), `never` (exception-like, no
  resume). *High.* (`std_core_hnd.html`)

### Evidence passing & Perceus interaction

- Compilation pipeline: multi-prompt delimited control →
  generalized evidence passing → yield bubbling → monadic
  translation to plain lambda calculus → C/JS. A hidden **evidence
  vector** `evv<e>` is threaded through effectful code; each entry
  pairs a unique `marker` with a handler pointer, so an op is
  dispatched by **O(1) lookup + direct call** — no dynamic stack
  search. *High.* (`multip.pdf`; `std_core_hnd.html`)
- **Type-selective transform / pay-as-you-go**: functions proven
  *total* (never yield) skip the monadic binds entirely — the
  non-yielding fast path **allocates nothing and preserves tail
  calls**; heap continuation-building happens only along an actual
  yield. *High, and directly relevant to Fern's cold-start goal.*
  (`multip.pdf`)
- Perceus (PLDI 2021): precise compiler-inserted `dup`/`drop`
  gives reference counting with **no GC and no runtime system**;
  reuse analysis turns a unique `drop` into in-place reuse (FBIP).
  *High.* (`xnning.github.io/papers/perceus.pdf`) — this is the
  same RC family Fern is adopting (`RC-PERCEUS-PLAN.md`).
- Because resumptions are ordinary RC'd heap values, a
  **multi-shot** resumption must `dup` the captured continuation
  before each re-invocation; an unused one is `drop`-ped (how
  `never` discards without leaks). *Medium* — structurally certain
  from the RC model, not quoted verbatim.
- **For async specifically, only one-shot (and zero-shot)
  resumption is needed**: `await` resumes exactly once when the
  callback fires; cancel resumes zero times. So async never pays
  the expensive multi-shot/dup path. *Medium-high.* (async TR;
  v1 `await-setup` "done" flag)

### Status & fit

- Koka v3 is a **research language "not quite ready for production
  use"** (latest v3.2.3, 2026-03-17). The current **C backend does
  NOT ship working async** — "Port `std/async` with `libuv`
  integration" is an open TODO. The fully-working `std/async`
  lives only in the **v1-master JS/C# backend** and rides the
  JS/.NET event loop. *High.* (`koka-lang/koka` readme; discussion
  #227; `lib/std/async.kk`)
- No built-in multi-threaded scheduler/parallelism; concurrency is
  cooperative single-threaded interleaving around async ops.
  *Medium* (absence claim).

**Takeaway for Fern:** Koka is the strongest evidence that
**colorless, direct-style async via effect handlers compiles to
native AOT code with no stack switching and no GC**, on the *same*
Perceus RC substrate Fern is building. The expensive part
(multi-shot dup) is exactly the part async never needs. This makes
"algebraic effects as the eventual colorless substrate" credible —
but Koka's own async being a still-unfinished TODO on the native
backend is a caution against making effects the *first* step.

---

## 2. Lean 4 — `Task`, OS-thread pool, sign-bit RC

Lean is the "industrial RC-compiled functional language with real
concurrency shipping today" data point. Its model is deliberately
*different* from what Fern wants, which is itself instructive.

- **Lean tasks run on OS threads from a runtime thread pool — NOT
  green threads, no M:N userland scheduler.** A task that blocks
  ties up a real OS pool thread. *High.* (lean reference "Tasks
  and Threads"; Zulip deadlock thread)
- The pool is a C++ `task_manager` (`src/runtime/object.cpp`):
  `std::vector<unique_ptr<lthread>> m_std_workers` + one
  `std::deque<lean_task_object*>` per priority level. Default size
  = logical-core count, overridable via `LEAN_NUM_THREADS`; soft
  limit (may exceed to avoid deadlock). *High* (source) / *medium*
  (soft-limit, from doc snippet).
- `Task.spawn` makes a pure task; `lean_task_spawn_core` enqueues
  via `g_task_manager->enqueue`. Dependencies are explicit:
  `Task.bind`/`map` call `add_dep` so a continuation won't run
  before its input is ready. **`IO.wait` does NOT register a
  dependency → waiting on a task from inside a pool task can
  deadlock** when threads are scarce (documented pitfall). *High.*
- `Task.Priority` = `default`/`max`/`dedicated`; **`dedicated`
  spawns the task its own OS thread off-pool** — the recommended
  idiom for blocking/long-running I/O so it doesn't starve the
  core-sized pool. *High.* (source + reference)
- `IO := EIO IO.Error`, a state-passing monad over a phantom
  `RealWorld`. No async/await keywords; the surface is
  spawn → `Task` handle → `bind`/`map`/`wait`. `IO.Promise` is a
  write-once cell whose result is a `Task` — the primitive that
  bridges external completions into the Task world. *High.*

### Reference counting & concurrency (the clever bit)

- `lean_object` header has a signed `int m_rc`: **`>0` =
  single-threaded (plain non-atomic RC), `<0` = multi-threaded
  (C11 atomic RC), `==0` = persistent (no RC).** RC ops branch on
  the sign; ST objects pay no atomic cost. *High.* (`lean.h`)
- Sharing a value with a task calls **`lean_mark_mt`, which
  negates the refcount and recursively marks the whole reachable
  graph MT** — thereafter every inc/dec on it is atomic.
  Consequence: ST code is cheap, but handing data to a task walks
  its entire object graph once and makes it permanently atomic →
  biases toward "share little, coarse-grained." *High* (mechanism)
  / *medium* (cost characterization, ours). NB: related to but
  *not* the academic "biased reference counting" algorithm — don't
  conflate.

### Async I/O & fit

- Default I/O is **blocking syscalls on the thread pool** (e.g.
  `IO.Process.run` blocks its thread). A **libuv-based async
  framework** (`Std.Internal.UV`, `Std.Internal.IO.Async`) was
  added v4.17 (2025-03) — timers, then async TCP (v4.20), UDP,
  channels — completing ops by resolving an `IO.Promise`. It is
  still namespaced `Internal`/unstable. So Lean has **both** a
  blocking-on-pool model (default) and an opt-in libuv event loop.
  *High* (coexistence) / *medium* (still-Internal).
- AOT-compiled to C → native, RC runtime, no tracing GC. The task
  pool is lazily/globally initialized — a program that never
  spawns pays no pool spin-up; once started it allocates ~one
  worker per core. *High* (AOT/no-GC) / *medium* (lazy-init
  inference).

**Takeaway for Fern:** Lean's per-core OS-thread pool is **the
wrong default for cold-start edge handlers** (the doc itself
recommends `dedicated` off-pool threads for blocking I/O — i.e.
the pool is a poor fit for blocking work, which is most edge I/O).
But two ideas transfer cleanly: (a) the **sign-bit ST/MT RC split**
is directly applicable — Fern's arena-per-handler, no-cross-handler-
sharing model means handler data can stay ST/non-atomic *by
construction*, dodging Lean's mark_mt graph-walk entirely; (b)
**`IO.Promise` as the bridge from a host callback into the
language's task world** is exactly the seam Fern's reactor needs.

---

## 3. Roc — push effects to the host, keep the language pure

Roc is the "where should the event loop live" data point, and the
closest match to Fern's existing `PLATFORM-RESEARCH.md` posture.

- **Roc's stdlib has no I/O; every I/O primitive comes from the
  platform.** A *platform* = a *host* (Rust/Zig/C) + a Roc-facing
  API. The **host owns `main()` and runs first**, then calls the
  compiled Roc app (an ordinary pure function); the host also
  provides alloc/free. The platform has **exclusive control over
  I/O — no in-language escape hatch.** *High.*
  (`roc-lang.org/platforms`, `/faq`)
- **Concurrency is the host's job.** Since the platform implements
  I/O, it also schedules it; the app *describes* what may run
  concurrently and the platform decides how. *High (as design
  intent).* (`/platforms`, `/functional`)
- **Shipped reality lags the vision**: `basic-webserver` uses Rust
  `hyper` + `tokio` in the host, but **runs each request on its
  own blocking thread** as an explicit workaround "until Roc has
  effect interpreters." So today's real concurrency = OS threads
  spawned by the host, one per request. *High.*
  (`basic-webserver` README; deepwiki)
- **`Task` is deprecated → "purity inference"** (Feldman,
  Oct 2024). `Task` → `Result`; the `!` suffix is part of an
  effectful function's *name*, `=>` (vs `->`) marks an effectful
  function type, and the compiler **infers** purity. *High.*
  (`roc-lang.org/plans`; basic-cli releases)
- Intended runtime mechanism: the **effect-interpreter model** —
  on each effect the Roc app returns a tag union to the platform
  containing the call args + a **continuation closure**; the
  platform performs the effect (possibly async) and calls the
  continuation with the result. Analogous to a Rust async state
  machine, but the *state machine value crosses the host
  boundary*. *High (design)* / *medium (whether fully
  implemented)* — could not confirm a ship date; `basic-webserver`
  implies not-yet at time of writing. (`/functional`, `/plans`)
- AOT via LLVM (also wasm); automatic reference counting, no
  tracing GC; final binary = host linked with compiled Roc app.
  No primary startup/binary-size numbers found (treat quantitative
  startup claims as unverified). *High* (model) / *low* (numbers).
- **No language-level concurrency primitives** (no threads,
  async/await, goroutines, actors); the in-language concurrency
  model was still an open community question (issue #5640). *High.*

**Takeaway for Fern:** Roc validates **"keep the event loop in the
host / platform-glue layer, keep the language surface pure-ish and
colorless above it"** — precisely Fern's existing direction. The
effect-interpreter "return a continuation to the host" model is
*one* way to implement the reactor seam; Fern's stackless-CPS
tasks are the same idea kept *inside* the binary rather than
marshalled across a host ABI (Fern controls all backends, so it
needn't pay Roc's host-boundary marshalling cost). The cautionary
note is loud: Roc's elegant host-scheduled-async story is **still
not shipped** — the pure-language-pushes-effects-to-host design is
harder to land end-to-end than it looks, and the fallback
(thread-per-request) is exactly the cold-start-hostile thing Fern
wants to avoid.

---

## 4. Broader AOT survey — the mechanism menu

### Go — stackful goroutines + M:N GMP scheduler
- Goroutines start ~2 KB, **growable contiguous (copying) stacks**;
  M:N over OS threads; GMP = goroutine / OS-thread / logical-
  processor-with-runqueue. User-space switch ~50–100 ns vs
  ~1–2 µs for an OS thread (order-of-magnitude reliable; exact ns
  secondary). *High/medium.*
- **The runtime (scheduler + GC) is mandatory and always linked**;
  GC spawns background goroutines and can't start until the
  scheduler is up. Hello-world binary ~1 MB+. *High.* → **directly
  in tension with Fern's small-binary/fast-startup goals.**

### Rust — stackless async, state machines, `Future`
- `async fn`/`async {}` compile to a **compiler-generated state
  machine** implementing `Future`; each `.await` is a state
  transition / suspend point. `poll(self: Pin<&mut Self>, cx) ->
  Poll<T>`; a `Waker` signals readiness. *High.*
- **No executor in std** — bring your own (tokio/smol/embedded).
  "Zero-cost but not free": no heap alloc, no mandatory runtime
  (good for small binaries / embedded), but you pay `Pin`, self-
  reference machinery, and the chosen executor. Futures are inert
  until polled. *High/medium.*
- Generated futures are often **self-referential** (a local
  borrows another across `.await`) → must not move once polled →
  hence `Pin`. *High.*
- Async functions are **colored** (red): `.await` only inside
  async context; sync can't transparently call async. *High.*

### Zig — had colorless async, removed it, now "I/O as interface"
- Zig 0.5–0.10 had **colorless** async/await: the compiler turned
  async functions into **stackless coroutines** (state machine +
  heap frame) via `suspend`/`resume`/`anyframe`, inferring which
  functions were async by call-graph traversal. *High.*
- Removed ~0.11 (`std.event.Loop` deleted). **Why:** LLVM's
  coroutine-splitting pass was "one of the slowest things LLVM
  does in debug builds" and "optimizations completely disabled due
  to LLVM bugs with coroutines" (#802); LLVM's "coroutine
  allocates its own memory" paradigm forced calls to be fallible
  and a hidden allocator param, blocking caller-controlled frame
  placement. *High — quoted from issue #2377.*
- New direction (2024–2026): **`Io` passed explicitly as a
  parameter** (like `Allocator`); caller picks
  `std.Io.Threaded`/evented backends with no recompile →
  **colorless without a keyword**. Evented backends on io_uring
  (Linux) / GCD (macOS). *High (Io-param)* / *medium (backends,
  versions).*

**The Zig lesson is the sharpest warning for Fern:** *do not lean
on a backend's coroutine-splitting pass.* Zig got colorless async
*for free* from LLVM coroutines and still tore it out because the
codegen was slow/buggy and the allocation model was wrong. Fern
emits its own machine code (no LLVM) and should own the stackless
transform **in its own IR**, where it controls frame placement and
debug-mode codegen — exactly the control Zig lacked.

### Colored vs colorless
- Coloring (Nystrom 2015): red=async can only be called from red →
  async "infects" call chains; sync/async compose poorly.
  Colorless: Go, old-Zig, new-Zig-`Io`, OCaml-effects. Colored:
  JS, Python, C#, **Rust**, Kotlin `suspend` (halfway). *High.*
- The colorless approaches all move the suspend mechanism **out of
  the function signature** — into a runtime scheduler (Go), an
  effect handler (OCaml), or a passed-in interface (Zig). *Medium
  (synthesis).* → Fern's `concurrent { }` does the same: suspension
  is a property of the *block*, not the function type.

### Stackful vs stackless — the cross-backend cost
- Memory/task: stackless ≈ tens–hundreds of bytes (state struct +
  captured locals); stackful reserves a whole stack (~4 KB–2 MB).
  *Medium (exact ranges secondary).*
- **Stackful resume needs architecture-specific hand-written
  assembly** for the register-save/stack-switch (Boost.Context
  `jump_fcontext` ≈ 24 instrs; or `swapcontext`/`SwitchToFiber`) —
  **cannot be written in portable C.** **Stackless coroutines are
  pure compiler IR transforms, inherently portable across
  targets.** *High.* → **This is the decisive cross-backend
  argument:** stackful = a separate asm stack-switch per ABI
  (arm64 + x86-64 + a wasm story that doesn't natively exist);
  stackless = one transform in `internal/ir`, all backends inherit.

### Structured concurrency
- Tasks confined to a lexical scope; the scope doesn't exit until
  all children finish → no leaks, errors propagate. Lineage:
  Sústrik (libdill ~2016) → Trio "nursery" (Smith 2017) → Kotlin
  `CoroutineScope` → Swift (2021, + actors). *High.* → This is the
  *composition* story `CONCURRENCY-RESEARCH.md` already chose;
  orthogonal to the *mechanism* (stackless vs stackful) chosen
  here.

### OCaml 5 — effects + Multicore
- Separates **parallelism** (domains ≈ OS threads) from
  **concurrency** (effect handlers / fibers). Delimited
  continuations are implemented on **fibers: small heap-allocated
  growable stack chunks**. *High.* (arXiv 2104.00250)
- **Continuations are one-shot** (resuming twice raises), enforced
  dynamically — which lets OCaml implement them in *direct style*
  and makes capture cheap (just a reference to the fiber). *High.*
- Effect handlers generalize exceptions (the handler also gets the
  resumable continuation) → **colorless direct-style async**
  without marking functions `async`; a top-level handler supplies
  the scheduler. *High.* Caveat: 5.0–5.2 have **no static effect
  typing** → unhandled effect is a *runtime* error. *Medium.*

**OCaml 5 vs Koka for Fern:** both give colorless effects. OCaml
uses one-shot fiber stacks (a *bounded* form of stackful — still
needs runtime stack-chunk management, though OCaml hides it).
Koka uses pure monadic/CPS yield-bubbling with *no* stack object
in the default backend. **Koka's strategy is the better template
for Fern** because it is a pure IR/codegen transform (portable, no
runtime stack management), and because Fern is already on the
Perceus RC substrate Koka targets.

---

## 5. WebAssembly — the constraint that decides the mechanism

WASM is the backend that *forces* the choice, because it has no
native stack switching.

- **Core wasm has a protected, non-addressable call stack:**
  structured control flow only, no `goto`, no `setjmp`/`longjmp`
  over the wasm stack, no snapshotting/relocating your own frames.
  You **cannot** natively suspend a deep call stack at an arbitrary
  I/O point. *High.* (stack-switching explainer; WasmFX paper)
- Three solution families: (a) **compile-time transform** the
  program to unwind/rewind itself (Asyncify); (b) **host** does the
  switch at an import boundary (JSPI; Wasmtime's `wasmtime-fiber`);
  (c) **add stack switching as a core primitive** (stack-switching
  proposal / WasmFX). *High.*

### Asyncify (Binaryen)
- Whole-program pass: instruments only functions that can reach an
  async import, spilling locals to a side stack in linear memory,
  driven by a global normal/unwinding/rewinding flag. Engine-
  agnostic (output is plain wasm). **Cost ≈ +50% avg, up to ~2×
  code-size and slowdown.** *High.* The portable fallback if you
  must suspend *untransformed* sync code.

### JSPI
- Suspend wasm at a JS-import boundary via `WebAssembly.Suspending`
  / `.promising`; the **JS engine** does the switch. **Only WASM
  frames may be on the stack between**; JS computations can't
  suspend. **Irrelevant to a non-JS WASI target.** *High.* Phase 4,
  Chrome 137 / Firefox 139 (versions secondary).

### Stack-switching proposal / WasmFX
- Adds first-class `(cont $ft)` + `cont.new`/`suspend`/`resume`/
  `resume_throw`/`switch` — **effect handlers as a core wasm
  feature**. Continuations are **one-shot**; multi-shot is only an
  open discussion. *High.*
- **Not production-ready for WASI:** Wasmtime's in-tree impl is
  experimental, flag-gated, **x64-only** (arm64/Windows deferred),
  missing `resume_throw`. The mature impl is a research fork
  (`wasmfxtime`). *High* (Wasmtime issue #10248).

### WASI poll + component-model async — **the pragmatic path**
- **WASI 0.2 gives async I/O with NO stack switching** via
  `wasi:io/poll.poll(list<pollable>) -> list<u32>` — a POSIX
  `poll(2)`-style readiness model. A guest builds a single-threaded
  reactor on top. *High.* **This is the key practical finding.**
- The component model adds **native async at the ABI level** with
  a **callback ABI (state-machine style)** that **does not require
  core stack-switching in the guest**; `stream<T>`/`future<T>`,
  backpressure. WASI 0.3 ships native async (Wasmtime 37+,
  ~Feb 2026; preview-vs-stable wording varies). *High (callback
  ABI)* / *medium (0.3 dates).*
- Wasmtime's *host-side* async rides its own `wasmtime-fiber`
  crate, **separate from** the core stack-switching proposal — so
  WASIp2/p3 async does **not** depend on the guest using stack
  switching. *High.*

### Threads on WASM
- The threads proposal adds only **shared memory + atomics, not
  thread spawning** (host must provide it). `wasi-threads` was
  withdrawn (~2023) for the still-evolving shared-everything-
  threads. WasmGC refs can't currently be shared across threads.
  *High.* → reinforces: **don't design Fern concurrency around
  WASM threads.**

### Practical verdict for Fern's wasm backend
- **For one short-lived edge request doing async I/O, you do not
  need stack switching at all.** If Fern's compiler CPS/state-
  machines `async`/`concurrent` itself (like Rust→poll, C#→state
  machine) and drives it from a `wasi:io/poll` reactor, you target
  wasm with **zero runtime stack switching, fully portable, no
  Asyncify tax.** *High.* (Rust-on-WASI is the proven design.)
- You'd only need Asyncify/JSPI/stack-switching if you wanted to
  suspend *untransformed* sync code at arbitrary depth — which the
  compiler-side transform makes unnecessary. *High.*

---

## 6. Synthesis — what fits Fern, and why

### The decision tree, collapsed

1. **Edge handlers need concurrency, not parallelism.** Overlap N
   I/O waits inside one request; the CPU is idle during the wait.
   → a **single-threaded readiness reactor** suffices. Parallelism
   (multi-core) is a *separate, later, host-scheduler* concern and
   must not be baked into the language surface (matches
   `CONCURRENCY-RESEARCH.md ▸ Rec §9`).

2. **The surface must be colorless** (no red/blue split). Every
   studied colorless design moves suspension out of the function
   signature. Fern's `concurrent { … }` makes suspension a
   property of the *block*; `await` inside it is structured, not a
   viral type. (Confirms `CONCURRENCY-RESEARCH.md ▸ Rec §2`.)

3. **The mechanism must be stackless.** This is the finding the
   three languages + the WASM constraint converge on:
   - Stackful (Go/OCaml-fiber) needs **per-ABI assembly** and a
     **per-task stack** → cross-backend cost + cold-start tax, and
     **no native WASM story**.
   - Stackless (Rust/C#/old-Zig/Koka-default) is a **pure IR
     transform** → lives once in `internal/ir`, every backend
     inherits, and **targets WASM with zero stack switching**.
   - Zig's removal of LLVM-coroutine async warns: own the
     transform in your *own* IR, don't rent a backend's.

4. **Don't adopt a general effect system yet — but aim the
   stackless transform so effects can land on it later.** Koka and
   OCaml 5 prove effect handlers give colorless async with no GC
   and (Koka default) no stack switching, on the *same Perceus RC*
   Fern uses. The expensive part (multi-shot resumption / `dup`-ing
   continuations) is exactly what async never needs — async is
   one-shot. So a future Fern effect system can reuse the
   one-shot-resumption stackless machinery; building the general
   system *first* is the expensive path Koka itself hasn't
   finished shipping.

### Concrete mechanism for Fern

```
concurrent {
    var a = task { plat.fetch(req_a) };   // suspends on I/O
    var b = task { plat.fetch(req_b) };   // suspends on I/O
    use(a.value, b.value);                // joins; scope-bounded
}
```

Lowering (all in `internal/ir`, target-agnostic):
- Each `task { … }` body compiles to a **stackless state machine**:
  live locals across an `await`/I/O point → an explicit heap
  record (RC'd, Perceus-managed); each suspension point → a state
  tag. (Rust `Future`/C# async shape.)
- A `task` value is a handle to that state record + a `pollable`
  (the host I/O readiness object) — directly mirrors Lean's
  `IO.Promise`-as-bridge.
- `concurrent { }` is the **nursery**: it owns its child task
  records, polls the reactor, and cannot exit until all children
  reach the terminal state (structured concurrency; Trio/Kotlin/
  Swift lineage). Scope exit cancels outstanding tasks (resume-
  zero-times, like Koka `cancelable`).
- The **reactor lives in the platform-glue layer** (Roc's "host
  owns the loop" insight, kept in-binary since Fern owns all
  backends): `wasi:io/poll.poll` on wasm, `epoll`/`kqueue` on
  arm64/x86-64. ~50 LOC, no scheduler in codegen.

### Why this beats each studied alternative for Fern

| Approach | Why not (for Fern) |
|---|---|
| Go goroutines (stackful M:N) | Mandatory always-linked scheduler+GC, ~1 MB binary floor, per-ABI stack switch, no WASM story. Cold-start hostile. |
| Rust async (as-is) | Right *mechanism* (stackless), wrong *ergonomics* (function coloring + `Pin`). Take the mechanism, drop the coloring via `concurrent {}`. |
| Zig old async | Right idea, but it depended on LLVM coroutine codegen and was removed. Fern owns its IR → take the idea, own the transform. |
| Lean OS-thread pool | Per-core thread pool + blocking I/O is cold-start hostile for edge; `dedicated`-thread-per-blocking-op doesn't scale to many concurrent waits. Borrow only the sign-bit RC idea + `IO.Promise` bridge. |
| Roc host-scheduled effects | Great "where the loop lives" precedent (adopt it), but the pure-pushes-to-host async is still unshipped and falls back to thread-per-request. Keep the tasks in-binary. |
| Koka effect handlers | The colorless end-game and proof it compiles AOT with no stack switch on Perceus RC — but the general effect system is heavier than needed *first*, and Koka's own native async is an open TODO. Reuse its one-shot stackless strategy; defer general effects. |
| OCaml 5 fibers | Colorless + one-shot, but fiber stacks are a (bounded) stackful runtime cost; Koka's pure-CPS strategy ports more cleanly to a multi-backend codegen. |
| Stackful coroutines (libdill/Boost) | Per-ABI assembly + per-task stack + no WASM. Disqualified by the multi-backend + wasm constraint. |
| WASM Asyncify | Only needed if you *don't* CPS-transform in the compiler. Since Fern will, it's an avoidable ~2× tax. Skip. |
| WASM stack-switching/WasmFX | The eventual "right" core-wasm answer, but experimental, x64-only in Wasmtime, one-shot, not production-ready. Don't depend on it; the poll reactor needs none of it. |

### The two findings that decide it

1. **Stackless = one IR transform for all three backends;
   stackful = three assembly stack-switchers + no WASM.** (§4
   stackful/stackless; §5 WASM.) This alone rules out the
   green-thread family for a multi-backend AOT compiler.
2. **A compiler-side CPS transform + `wasi:io/poll`/`epoll`/
   `kqueue` reactor needs no stack switching on *any* backend,
   including wasm.** (§5.) This is the proven Rust-on-WASI design
   and exactly what Fern's edge-handler workload requires.

Everything else — colorlessness, structured cancellation, the
host-owned loop, the eventual effect system — composes on top of
those two without changing them.

---

## Appendix — confidence & open items to re-verify

Decision-load-bearing claims are all **high-confidence and
cross-corroborated** (Koka default-backend monadic resumptions;
stackful-needs-asm; WASM-poll-needs-no-stack-switching; Roc
host-split; Lean OS-thread pool). These flagged items are **not
load-bearing** for the recommendation but should be re-checked if
cited externally:

- Exact goroutine context-switch ns (secondary sources).
- Zig async removal version (≈0.11) and 0.16 evented-`Io` timeline
  (primary release notes 403'd).
- JSPI phase/browser versions (Phase 4 / Chrome 137 / FF 139).
- WASI 0.3 / Wasmtime 37 ship date (~Feb 2026) and preview-vs-
  stable wording.
- Whether/when Roc's effect interpreters shipped end-to-end.
- Koka multi-shot-resumption-`dup` is structurally certain from
  the RC model but not quoted verbatim.

### Primary sources
**Koka:** `xnning.github.io/papers/multip.pdf` (generalized
evidence passing / yield bubbling); MSR "Generalized Evidence
Passing for Effect Handlers"; MSR "Structured Asynchrony with
Algebraic Effects"; `xnning.github.io/papers/perceus.pdf`;
`koka-lang.github.io/koka/doc/std_core_hnd.html`;
`github.com/koka-lang/koka` (readme, discussion #227);
`github.com/koka-lang/libmprompt`;
`raw.githubusercontent.com/koka-lang/koka/v1-master/lib/std/async.kk`.
**Lean 4:** `github.com/leanprover/lean4` —
`src/include/lean/lean.h`, `src/runtime/object.cpp`,
`src/Init/System/IO.lean`; lean-lang.org reference "Tasks and
Threads" + "Reference Counting"; release notes v4.17 / v4.20;
`arxiv.org/pdf/1908.05647` (Counting Immutable Beans).
**Roc:** `roc-lang.org/platforms`, `/functional`, `/plans`,
`/faq`, `/fast`; `github.com/roc-lang/basic-webserver`;
`github.com/roc-lang/basic-cli/releases`;
`github.com/roc-lang/roc/issues/5640`; Feldman "Functional Purity
Inference Plan" (Oct 2024).
**AOT survey:** `github.com/ziglang/zig/issues/2377`;
`doc.rust-lang.org/std/pin/struct.Pin.html`;
`rust-lang.github.io/async-book`; `go.dev/blog/go1.7-binary-size`;
`ocaml.org/manual/5.4/effects.html`; `arxiv.org/pdf/2104.00250`
(OCaml multicore); Nystrom "What Color Is Your Function";
`photonlibos.github.io/blog-20241014/...` (stackful asm);
`en.wikipedia.org/wiki/Structured_concurrency`.
**WASM:** `github.com/WebAssembly/stack-switching` (Explainer,
#110); `wasmfx.dev` + WasmFX paper; `github.com/WebAssembly/
js-promise-integration`; `emscripten.org/docs/porting/asyncify`;
`github.com/WebAssembly/wasi-io` (poll.wit);
`github.com/WebAssembly/component-model` (Async.md, CanonicalABI);
`github.com/bytecodealliance/wasmtime/issues/10248`;
`github.com/WebAssembly/shared-everything-threads`.
