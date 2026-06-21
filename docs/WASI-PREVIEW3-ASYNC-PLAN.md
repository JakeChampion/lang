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
  async** (behind these `-W` flags). **The pinned wasmtime is now
  v37.0.1** (CI `.github/actions/setup-fern/action.yml`): bumped from
  34.0.1 after verifying the whole Preview-2 wasm suite passes under 37
  (async stays off by default), so the toolchain prerequisite for P3 is
  satisfied — CI can run async components once codegen emits them.
- **wasm-tools 1.240.0** (bumped from 1.225, which **rejected** the
  `async func` WIT syntax). 1.240 authors async cleanly: both an
  `async func` interface method and an `export … : async func(...)`
  round-trip through `wasm-tools component wit`, and `future<T>` /
  `stream<T>` types already did. Verified the whole Preview-2 wasm suite
  still passes under 1.240 (+ wasmtime 37), so the bump is
  backward-compatible. So the async *authoring* path
  (validate/print/encode) is now available, not just the runtime.

**Conclusion:** the toolchain is ready — wasmtime 37 runs async
components (async off by default) and wasm-tools 1.240 authors/validates
them. Both are now the CI-pinned versions, so P3 async codegen has a
fully testable target (author + validate + run). What remains is the
**implementation** — emitting the async canonical ABI from the Fern
composer — not the tooling.

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
2. **Gate P3 on a toolchain bump. — DONE.** The pinned toolchain is now
   wasmtime v37.0.1 + wasm-tools 1.240.0 (CI + local), each verified to
   keep the existing P2 wasm suite green (async stays off by default;
   `-W component-model-async` is opt-in). wasmtime 37 runs async
   components; wasm-tools 1.240 authors/validates `async func` +
   `future`/`stream`. The toolchain prerequisite is fully satisfied —
   async codegen has a testable author→validate→run target.
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
