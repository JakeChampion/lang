# WASI Preview 3 (component-model async) — feasibility + phased plan

The wasm reactor shipped on **Preview 2 pollables** (timer/block/poll/
drop + the `std/wasm_reactor` scheduler — see docs/WASM-REACTOR-PLAN.md),
which runs on the stock wasmtime the rest of the suite uses. This doc
records what a move to **Preview 3 native async** (the component model's
`async` lift/lower + `future<T>` / `stream<T>`) would take, grounded in
a direct probe of the available toolchain, and recommends the staging.

## Why P3 at all

Preview 2 has no native async: readiness is a `pollable` resource and
the program drives a hand-written scheduler (our `std/wasm_reactor.run`
over `wasm_poll`). Preview 3 makes async a first-class component-model
capability:

- An import/export can be **`async`**: the canonical ABI lifts/lowers it
  with `canon lift async` / `canon lower async`, so the caller gets a
  `future`/`stream` it can await instead of a pollable to block on.
- New value types **`future<T>`** and **`stream<T>`**, with builtins
  (`future.new/read/write`, `stream.new/read/write`, `task.return`,
  `waitable-set.*`).
- The host can run many in-flight async calls and resume the guest when
  any completes — no guest-side poll loop.

For Fern's edge-handler use case this means a handler could `await`
overlapped outbound fetches directly, without the explicit `Step[T]`/
`wasm_poll` reactor.

## Toolchain probe (2026-06, this environment)

- **wasmtime 37.0.1** — `component-model-async` is present:
  `-W component-model-async`, `-W component-model-async-builtins` (🚝),
  `-W component-model-async-stackful` (🚟), plus async stacks +
  `component-model-error-context` (📝). So the **runtime supports P3
  async** (behind these `-W` flags). The repo's pinned wasmtime is
  **34.0.1**, which predates these — so CI (and `/root/.fern-wasm`)
  can't run async components until bumped.
- **wasm-tools 1.225.0** — partial: `future<u32>` / `stream<T>` WIT
  **round-trips cleanly**, but the **`async func` WIT syntax is
  rejected** (`expected keyword 'func', found an identifier`). So the
  async *types* are encodable, but the async *function modifier* in WIT
  text is not yet in this version. Binary `canon lift/lower async` may
  still be hand-emittable (the Fern composer emits component bytes
  directly, not WIT text), but validation/printing of an async lift is
  not yet exercised by our tooling.

**Conclusion:** the runtime is ready; the authoring/validation tooling
is immature, and CI is two minor versions behind the async runtime.
Nothing about P3 is testable in CI today without a wasmtime bump, and
even locally a hand-emitted async lift can't be fully WIT-validated.

## Scope of a P3 implementation in Fern

Far larger than the P2 pollable reactor (which reused existing
composition + three small builtins). It would touch:

1. **Component composer** (`internal/wasm/component`): emit `canon lift
   async` for an async export and `canon lower async` for async imports;
   synthesize `future<T>` / `stream<T>` defvaltypes; wire the async
   builtins (`task.return`, `future.read/write`, `waitable-set.wait`,
   `subtask.drop`). This is comparable in size to the *entire* existing
   preview-2 composition surface, plus the async state machine.
2. **wasmbin** (`internal/codegen/wasmbin`): emit the core funcs that
   call the async builtins; a return-area / waitable discipline for
   in-flight subtasks.
3. **Language surface**: an `async` / `await` shape (or reuse the
   `concurrent { … }` desugar) that lowers to async lower + future read,
   rather than to the `Step[T]` reactor.
4. **Tooling/CI**: bump wasmtime to ≥37 with `-W
   component-model-async`, and either a newer wasm-tools that validates
   async lifts or a validation shim. The whole `internal/e2e` wasm path
   asserts against the pinned 34 today.

## Recommendation (staging)

1. **Stay on the Preview-2 reactor for now** — it is complete, tested,
   and CI-green on stock wasmtime, and it already delivers the
   edge-handler fan-out (timer + poll + scheduler + deadline). The next
   concrete async increments (outbound socket pollables, a `select`
   first-wins combinator) all build on it with no tooling risk.
2. **Gate P3 on a toolchain bump.** The first P3 PR should be
   infrastructure: bump the pinned wasmtime to ≥37 and confirm the
   existing P2 wasm suite still passes under it (async stays off by
   default; `-W component-model-async` is opt-in), and pull a wasm-tools
   that validates async lifts. Only once that is green does async
   codegen have a testable target.
3. **Then implement P3 incrementally**, smallest-first: a single
   `async` export returning a `future<u32>` resolved immediately
   (`task.return`), run under `wasmtime -W component-model-async` — the
   async analog of the first P2 timer-pollable slice — before any
   `future`/`stream` plumbing or language surface.

The P2 reactor and a future P3 path are complementary: P2 is the
portable, here-now scheduler; P3 is the native-async future that becomes
worthwhile once the runtime ships stable and the authoring tooling
catches up. This doc is the checkpoint so that effort starts from a
known position.
