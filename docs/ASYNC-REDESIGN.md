# Async redesign — futures + combinators, not parser CPS

> Status: **accepted direction**, migration in progress. This document
> supersedes the *surface-syntax + parser-CPS* parts of
> `docs/ASYNC-IMPLEMENTATION-PLAN.md`. The **runtime** pieces that plan
> describes (the reactor, real `poll`, `register_fd`, the
> component-model-async import path) are kept — see *Keep / cut* below.
> Written with no users in the field, so we are free to remove the
> `concurrent` / `await` / `race` surface rather than evolve it.

## TL;DR

Fern accidentally grew **two** async systems:

1. **Component-model-async imports** — `@import(...) async function`,
   lowered in the **IR** (`ExternFunc.Async`, `canon lower async`,
   future/stream result elems). Colorless, platform-native, clean. This
   is the WASI-Preview-3 path.
2. **The `concurrent` / `await` / `race` desugar** — ~318 lines of
   source-to-source CPS in `internal/parser/parser.go` (~5% of the
   file), splitting task-function bodies at every `await` into
   continuation closures over `std/task`. No IR representation; not
   mirrored in the self-host parser; the most regression-prone surface
   in the whole feature.

System 1 is where the platform and the industry are going. System 2 is
a bespoke compiler-construction hazard. **We collapse onto one
abstraction — `future<T>` — exposed as structured-concurrency
combinators (`gather` / `race` / `with_deadline`), and we delete the
parser CPS transform.**

## The problem with parser-side CPS

Doing the coroutine transform on the **AST, in the parser**, is the root
of the cost regardless of execution quality:

- **Every control-flow shape needs bespoke handling.** Loops become
  recursive functions with carried mutated vars; conditionals use a
  "push the REST into both branches" merge; expression-position awaits
  need a hoist pre-pass; break/continue need terminator rewriting. This
  is inherent to source-to-source CPS — you are fighting surface syntax
  instead of a normalized control-flow graph.
- **It runs before type-checking**, so it can't use type information.
- **It must be re-implemented in the self-host parser** to ever retire
  the Go compiler — so it directly taxes goal 1 (full self-host IR) and
  roughly *doubles* in maintenance the day that port starts (today:
  zero of the 318 lines are mirrored in `examples/self_host/parser.fern`).
- **It is the feature's most fragile surface** (loops × conditionals ×
  expression-position × break/continue is a large cross-product, each
  cell hand-written).

No compiler with a real IR does coroutines in the parser. Rust lowers
async on MIR (a CFG); C# builds a state machine over a CFG; Kotlin does
CPS over an IR. On a CFG, "loops/branches/breaks" are already just
edges, so "assign a state to each suspend point and switch on state" is
*one uniform transform*, not N special cases. **If we ever want
transparent `await`-anywhere again, it belongs in `internal/ir`, never
the parser.**

## Do we even need await-anywhere?

The stated use case is narrow and stereotyped: an edge handler issues
1–10 outbound fetches and gathers or races them, optionally with a
deadline. That is `gather` / `race` as **library functions** — which is
essentially what `std/reactor.run_io` / `run_io_select` /
`run_io_deadline` (native) and `std/wasm_reactor.run` / `select` /
`run_deadline` (wasm) already are.

```fern
// today (CPS surface, deleted by this redesign):
concurrent {
    spawn a = fetch(c1);
    spawn b = fetch(c2);
    await a; await b;
}

// after (combinators, no compiler transform):
let bodies = gather([fetch(c1), fetch(c2)]);   // explicit fan-out point
for b in bodies { process(b) }
```

The combinator form is slightly less sugary for pipelines but is
**colorless**, makes the concurrency point **explicit** (the spirit of
structured concurrency), needs **zero compiler magic**, and keeps the
"no stackful green threads / has a wasm story" property the original
design correctly required. For the actual workloads, that trade is
strongly positive.

## Target architecture: one `future<T>`

A single async abstraction, defined once and implemented per backend:

- **A `future<T>` is a not-yet-ready value of type `T`.**
- **wasm (Preview 3):** futures are *native* to the component model.
  `wasi:http` returns them; the host scheduler suspends our
  async-lifted `handle` export. We get real async from the platform we
  are betting on, with the host doing the work. This reuses the
  existing `@import async` / `canon lower async` path.
- **native:** a `future<T>` is "an fd plus a continuation that yields
  `T` once the fd is ready" — **exactly the `register_fd` / `IoStep`
  machinery already built and proven** (`reactor_socket_test.go`,
  `task_real_fd_test.go`). The reactor stays; it becomes the native
  implementation of one IR concept.
- **interp:** no real fds, so a `future<T>` resolves only via the
  in-memory completion path (the simulation that already exists in
  `std/task`). Useful for tests; real I/O is native/wasm.

`gather([future<T>]) -> [T]`, `race([future<T>]) -> (i32, T)`, and
`with_deadline(ms, [future<T>]) -> [Option<T>]` are combinators over
this one type. They are the entire user-facing concurrency surface.

This unifies the two accidental systems: the same `future<T>` underlies
both a `wasi:http` async import (wasm) and a `tcp_connect`-driven fetch
(native), and `gather`/`race` work identically over either.

### Generic over `T`, not i32-throughout

The current `std/task` runtime is **i32-throughout** specifically to
avoid generics in the scheduler. A first-class `future<T>` removes that
restriction: the native compiler already monomorphizes generics, and
`std/wasm_reactor.Step[T]` / `std/reactor.IoStep[T]` already prove the
generic shape compiles on both backends. So `gather` over
`future<string>` (response bodies) is a first-class case, not a
hand-duplicated `…Str` twin.

## Surface API (the blessed library)

A new `std/async` module presents the portable surface; it dispatches to
the native reactor or the wasm pollable reactor underneath.

```fern
// A pending computation yielding T. Construct via the I/O primitives
// (fetch, socket reads) or @import async functions; never by hand in
// normal code.
pub struct Future[T] { /* backend-specific payload */ }

// Await ALL — fan out, collect every result in input order. The
// canonical edge fan-out (cache + primary; issue N, take all).
pub function gather[T](fs: Future[T][]): T[]

// Await FIRST — happy-eyeballs / race. Returns (winnerIndex, result);
// the losers are dropped (structural cancellation — never resumed).
pub function race[T](fs: Future[T][]): (i32, T)

// Await ALL within a wall-clock budget. A future that misses the
// deadline lands as None (a cancelled straggler).
pub function with_deadline[T](ms: i32, fs: Future[T][]): Option[T][]
```

`std/fetch` grows future-returning constructors (non-blocking start +
read-on-ready), e.g. `fetch_future(host, port, path): Future[string]`,
so the canonical program is:

```fern
import "std/async";
import "std/fetch";

function handle(): i32 {
    let bodies = async.gather([
        fetch.fetch_future(cache, 80, "/k"),
        fetch.fetch_future(primary, 80, "/k"),
    ]);
    // both sockets overlapped on one thread; bodies in input order
    return bodies.len();
}
```

No `await` keyword, no `concurrent` / `race` blocks, no function
coloring, no parser transform.

## Keep / cut

**Keep (good bones):**
- The reactor: `register` / `register_fd` / `poll_ready` / `run` /
  `select` and the native real-fd scheduler (`std/reactor`), the wasm
  pollable scheduler (`std/wasm_reactor`), the deadline variants.
- The `poll` builtin (universal: real on native, `-1` stub on
  interp/wasm) and `timer_fd` / `monotonic_ns`.
- The component-model-async import path: `@import async function`,
  `ExternFunc.Async`, `canon lower async`, future/stream result elems.
- `plat.fetch -> i32` status and the `http_status` / `http_body`
  parsers.

**Cut:**
- The parser CPS transform: `desugarTaskFunctionsProgram`,
  `transformTaskFunction`, `buildTaskSegment`, `lowerTaskIfMerge`,
  `buildTaskWhile` / `buildLoopBody` / `wrapLoopReturns`,
  `hoistTaskExprAwaits` / `rewriteAwaitExpr`, `parseRaceExpr`, and
  helpers (~318 lines of `parser.go`).
- The `await` / `concurrent` / `race` keywords and the `ast.Await`
  node.
- The desugar's gates and corpus/e2e tests that assert the transformed
  output (`async_task_fn_test.fern`, the `concurrent`/race runner
  gates, `TestParseTaskFunctionDesugar` / `TestParseRaceDesugar`).
- The i32-throughout restriction in the scheduler, once `Future[T]` is
  generic.

**Re-home (becomes the implementation detail of `Future[T]`, not a
user surface):** `std/task`'s `Step` / `Reactor` — kept as the native
in-memory/fd reactor that `std/async` wraps, but no longer the thing
user code or a desugar targets directly.

## Migration plan (sliced; one PR each)

1. **PR1 — this doc.** Direction + plan. *(in progress)*
2. **PR2 — `std/async`: portable `Future[T]` + `gather` / `race` /
   `with_deadline`.** A self-contained module (its own `Future[T]`
   enum + combinators over the universal `poll` builtin — it does not
   depend on `std/reactor`/`std/task`, which fold in at PR5). The new
   blessed surface. Also **retires the `race` keyword** here, since the
   `race` combinator is its direct replacement and the keyword
   otherwise shadows the function name (the rest of the CPS surface —
   `concurrent`/`await` — is removed in PR4). Tests: portable
   `Ready`-future `gather`/`race`/`with_deadline` on interp + wasm;
   native real-fd `gather`/`race` over sockets (x86-64 + arm64).
3. **PR3 — port examples + e2e to combinators.** Move the edge-handler
   examples to `std/async`; prove one real overlapping fetch through
   `gather` end-to-end (x86-64 + arm64). Add `fetch_future`.
4. **PR4 — rip out the parser CPS.** Delete the transform, the
   `await`/`concurrent`/`race` keywords + `ast.Await`, and the desugar
   gates/tests. Net-negative LOC; unblocks self-host (no giant desugar
   to mirror).
5. **PR5+ — promote `future<T>` to the IR.** Make it a first-class IR
   type; native lowers it over the reactor, wasm over
   component-model-async. Fold `std/reactor` / `std/wasm_reactor` /
   `std/task` into the one abstraction. The deep unification; deferred,
   sits alongside the goal-1 IR work.

Each slice: branch → commit → push → PR → subscribe → merge on green.
PRs 2–4 are mechanical and low-risk given the reactor already exists and
is proven; PR5 is the real design work and lands incrementally.

## Tradeoffs / open questions (deliberately deferred)

- **No transparent `await` in arbitrary control flow.** Accepted: the
  use case doesn't need it, and if demand appears it returns as an
  IR-level state-machine pass, not parser CPS.
- **`Future[T]` payload shape across backends.** PR2 can use a tagged
  union (in-memory value vs fd+continuation); PR5 replaces it with the
  IR type. The combinator *signatures* are stable across that change —
  that's the point of fixing them first.
- **Cancellation** stays structural (a dropped future is never
  resumed; RC/Perceus reclaims its frame). PR5's IR future additionally
  closes a loser's fd to stop in-flight OS I/O.
- **`with_deadline` return type** (`Option[T][]`) assumes an
  `Option`/nullable in the stdlib; if absent we fall back to a
  sentinel + parallel `bool[]` ready-mask, decided in PR2.
