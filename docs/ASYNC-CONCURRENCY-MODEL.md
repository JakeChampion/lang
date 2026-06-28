# Async concurrency model — colored vs colorless (decision pending)

Fern's async surface today is **colorless-sequential**: an `@import async
function f(): T` auto-awaits and the call site yields a plain `T` — no `await`
keyword, no `future[T]` in source (`docs/WASI-PREVIEW3-ASYNC-PLAN.md`,
`docs/STREAM-TYPE-SURFACE.md`). That shipped end-to-end (scalar/string/list/record
results + params, `stream[T]` result/param, and lazy `for x in stream` for every
scalar element). This note records the **next** open question before the surface
ossifies: is colorless the right model, or should Fern adopt **colored** async
(`async`/`await` + surfaced `future[T]`)? It is written so the decision is made
from a stated position, like the encoder / stream-surface checkpoints before it.

**Status: undecided — awaiting a language-direction call.** This note frames the
choice and records a recommendation; it does not commit any implementation.

## The real axis is the concurrency model, not the keyword

"Colored vs colorless" is downstream of "how do you express concurrency." Three
coherent points in the space:

1. **Colorless-sequential** — *what Fern has now.* Auto-await, no keyword.
   `var a = fetch(x); var b = fetch(y);` runs **strictly sequentially**: there is
   no way to overlap IO and no concurrency primitive at all.
2. **Colored** (Rust / JS / C#) — `async`/`await` + `future[T]`. Concurrency via
   `join` / `race` / `select`. Coloring is viral up the call graph.
3. **Colorless + structured spawn** (Go / Java-Loom) — no `await` keyword, any
   function may block, concurrency via `spawn` + structured scopes + channels. No
   coloring, but needs stackful suspension.

So the decision is really: *is (1) enough; and if not, do we want await-composition
(2) or spawn (3)?*

## The substrate is already colored

WASI Preview-3 component-model-async is colored at the ABI: `canon lower async`
yields a subtask/status awaited through waitable-sets (`waitable-set.wait`). The
e2e harness already runs wasmtime with `component-model-async-stackful`. So:

- A colored surface (2) is a *thin, faithful* mapping — `future[T]` ≈ subtask,
  `await` ≈ the waitable-set wait. The colorless model is a layer of auto-inserted
  await loops on top; the lazy-stream cursor (`__stream_next` per loop turn,
  `docs/STREAM-TYPE-SURFACE.md`) is essentially per-step `await` smuggled in under
  a colorless skin — a sign the abstraction leaks once iteration is involved.
- A spawn model (3) needs exactly the stackful suspension the substrate already
  provides, so it is not a new runtime capability — it is a scheduler + scope API
  over the same waitable machinery.

## Colored — pros / cons (for Fern specifically)

**Pros**
- **Concurrency becomes expressible.** Edge handlers fanning out to N backends and
  combining results are the canonical case, and colorless-sequential *cannot*
  overlap them. Biggest single argument for changing anything.
- **Honest mapping to the P3 substrate** (above): less magic, fewer hidden
  suspension points; reuses the internal `future[T]` / subtask types.
- **First-class cancellation / timeouts / deadlines** compose (`race(work, timer)`)
  — directly relevant to edge handlers with budgets.
- **Visible suspension points** aid reasoning about ordering, reentrancy, and
  partial failure in a single-threaded runtime.

**Cons**
- **Viral coloring taxes the common case.** Fern targets small fast CLI tools that
  are mostly synchronous; coloring adds ceremony and tends to force a sync/async
  stdlib split.
- **Collides with the Perceus RC roadmap (goal #2).** Ownership across `await` is
  where Rust grew `Pin`/`Send`/borrow-across-await complexity. Colorless auto-await
  keeps suspension hidden and local, sidestepping it. Introducing colored await
  *while* porting reference counting to the self-host compiler multiplies two hard
  problems.
- **N× implementation surface.** Every feature must work on arm64 + x86-64 + wasm
  and be self-hosted; the colorless model is already green across all of them.
- **Over-serves the median handler** ("request in → a few awaits → response out"),
  whose payoff from coloring is small.

## Recommendation (pending sign-off)

For the stated goals (small fast CLI tools + short-lived edge handlers) and the
active Perceus RC work, the recommendation is **not full coloring, but also not
staying colorless-sequential** — the gap that actually bites is *no concurrency*.
Prefer **option 3: colorless + structured `spawn`/scope + channels**:

- buys edge fan-out concurrency **without** viral coloring or a sync/async stdlib
  split;
- keeps the "write straight-line code" ethos that motivated colorless;
- leans on the stackful P3 substrate already in use;
- keeps suspension out of the type system — far kinder to the incoming RC port
  than borrow-across-await.

Choose **full coloring (2)** only if first-class composable
cancellation / `select` / backpressure is judged a core language value worth the
viral cost and the RC-interaction complexity — a "systems language" stance more
than a "small fast edge tools" one.

## If option 3 is chosen — minimal first slice (sketch, not committed)

A structured-concurrency primitive over the existing waitable machinery:

- `spawn EXPR` starts an async call as an in-flight subtask and yields a
  `Task[T]` handle (internal — the `future[T]`/subtask the ABI already produces).
- A structured `scope { … }` (or `await_all` / `join`) joins every task spawned in
  it before the scope exits — no detached tasks, so cancellation/cleanup is
  lexical. Maps to one `waitable-set` per scope: `spawn` → `waitable.join`,
  scope-exit → drain via `waitable-set.wait` until all complete (the loop the
  collect/await wrappers already implement in `internal/codegen/wasmbin`).
- `race` / `select` over a scope's tasks returns the first completion and cancels
  the rest (`subtask.drop` on the losers) — the timeout/deadline primitive.

Codegen reuses `emitAsyncAwaitLoop` + the waitable-set intrinsics already wired
for the colorless await; the new work is the surface (`spawn`/`scope`), the
`Task[T]` type, and the scope→waitable-set lowering. No coloring of ordinary
functions; `spawn` is the only new suspension marker.

## Cross-references

- `docs/WASI-PREVIEW3-ASYNC-PLAN.md` — the P3 ABI + phased plan (the substrate).
- `docs/STREAM-TYPE-SURFACE.md` — the `stream[T]` surface + lazy `for x in stream`
  (where the colorless abstraction starts to leak).
