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

## Proven recipe — a minimal async export runs end to end

Spiked successfully: this component (authored as WAT, encoded by
wasm-tools 1.240) returns `42` under
`wasmtime run -W component-model-async,component-model-async-stackful --invoke 'run()'`:

```wat
(component
  (core func $task_return (canon task.return (result u32)))
  (core module $m
    (import "" "task-return" (func $tr (param i32)))
    (func (export "run") i32.const 42  call $tr))   ;; void return; result via task.return
  (core instance $libi (export "task-return" (func $task_return)))
  (core instance $i (instantiate $m (with "" (instance $libi))))
  (func $run (result u32) (canon lift (core func $i "run") async))
  (export "run" (func $run)))
```

Decoded canonical encodings (now emitted by the composer package, byte-
verified in `component_test.go`):

- **`canon lift … async`** = `00 00 <coreFuncIdx> 01 06 <typeIdx>` — the
  `async` canonical option is **0x06**. → `PutCanonSectionLiftAsync`.
- **`canon task.return (result u32)`** = `09 00 79 00`. →
  `PutCanonTaskReturnSingle`.
- An async-lifted export's core function returns **void** and calls the
  `task.return` core import to deliver its result; function-return =
  task done. It needs the **stackful** async feature at runtime
  (`-W component-model-async-stackful`).

**Status — DONE, Fern source → runnable async export via the CLI.**
(1) the canonical-async emitters (`PutCanonSectionLiftAsync` /
`PutCanonTaskReturnSingle`) + bytes tests + a runnable assembly test;
(2) the wasmbin async core-func shape (`BuildOptions.AsyncExportName`:
the `("", "task-return")` import + a synthetic `() -> ()` core func that
calls `main`, hands its i32 to task-return, returns void) + the composer
assembly (`component.BuildAsyncLiftedExportComponent`); (3) the CLI
surface — `fern -target wasm-bin -async-export` produces a component
exporting `run: async func() -> u32`, run with
`wasmtime run -W component-model-async,component-model-async-stackful --invoke 'run()'`.
Tests: `TestWasmP3AsyncExport{Assembly,FromFern}` + `TestCmdLangAsyncExport`
(CLI-driven, returns 42).

**UPDATE — first-class `async` keyword.** `async function foo(): i32 {
… }` (a contextual modifier, like `fip` — `async` stays usable as an
ordinary identifier elsewhere; `pub async function` works) marks the
function `FuncDecl.Async`. On `-target wasm-bin` the driver lifts the
async-marked function under its own name (`foo: async func() -> u32`),
no flag needed; the `-async-export` flag remains for wrapping `main` as
`run`. The source function is pinned past both the AST tree-shaker and
the IR-level cull (it's reachable only through the synthetic async
wrapper). Tests: `TestParseAsyncModifier` + `TestCmdLangAsyncFunctionKeyword`
(`async function compute(): i32 { return 6*7; }` → exported `compute`,
returns 42 under the async features; fails without them, confirming the
async lift).

## Next epic — the async IMPORT / await side (scoped, tooling-confirmed)

The export side is complete (above). The remaining P3 capability is the
async *import* — a guest that **awaits** a host/peer async function, the
colorless-await payoff. Probed under wasm-tools 1.240 + wasmtime 37:

- **`canon lower async` is authorable** (`(canon lower (func $dep) async
  (memory $m "mem"))`) — it **requires the `memory` canonical option**
  (the lowered call writes the subtask/return info into linear memory).
  A bare `canon lower … async` validates-fails with "canonical option
  `memory` is required".
- **The waitable builtins exist** (`canon waitable-set.new`,
  `waitable-set.wait`, …) — the await state machine the guest uses.

So the epic is tooling-feasible. The work, smallest-first:

1. **Spike the await state machine — DONE (sync case), proven end to
   end.** A self-contained **nested-component** component (provider
   nested inside consumer — no `wac`, no `wasm-tools compose`): the
   consumer imports the nested `dep: async func() -> u32`, lowers it
   `async` with a memory option, calls it, reads the result from the
   return area, and `task.return`s it; lifted `async` as `run`. Runs
   under `wasmtime -W component-model-async,component-model-async-stackful
   --invoke 'run()'` → **42**. Key findings: (a) the `async` lower
   **requires** the `memory` option; (b) the `memory`-option
   **circularity** (the lower needs the user module's memory before the
   module is instantiated) is sidestepped in the spike by externalizing
   memory into a shared core instance — the real composer reuses its
   existing memory-trampoline machinery (the same one P2 `gMem`
   lowerings use); (c) for a **synchronously-completing** import the
   lowered call writes the result inline and **no `waitable-set` loop is
   needed** — only a *pending* import needs `waitable-set.wait`. The
   `canon lower async` encoding is decoded and now emitted by
   `component.PutCanonSectionLowerAsync` (`[async 0x06, memory 0x03]`),
   byte-verified against the spike (`TestPutCanonSectionLowerAsync_Bytes`).
1b. **Await proven through the Go composer — DONE.** The whole
   consumer-awaits-a-nested-async-provider component is now assembled via
   the Go composer (no wac, no wasm-tools compose), using the
   nested-component encoders (`PutComponentSection` /
   `PutInstanceSectionInstantiateComponent`) + `PutCanonSectionLowerAsync`
   + a hand-built consumer core (lower the import, read the shared return
   area, `task.return`). Runs under wasmtime async features → 42
   (`TestWasmP3AsyncImportAwait`), exercising BOTH async-ABI directions
   (lower + lift). So the await path is a permanent CI artifact, not just
   a `/tmp` spike.
2. **Composer**: a `BuildAsyncLowerImport` path emitting `canon lower
   async` + the memory option + the waitable wiring; thread the imported
   async func through the existing import-composition machinery — using
   the memory-trampoline (the `/tmp`/test uses an externalized shared
   memory; the real composer reuses the P2 `gMem` trampoline so a
   single-memory program works).
3. **wasmbin**: the core funcs implementing the await loop (the sync case
   needs only the lowered call + a return-area read — proven; a *pending*
   import additionally needs `waitable-set.*` + the subtask poll), and a
   runtime helper exposing "call this async import and block until ready".
4. **Fern surface — DECIDED: colorless auto-await.** An `async`-marked
   import call is implicitly awaited — `var x = dep();` just returns the
   value, the compiler inserts the await (no `await` keyword; matches the
   research doc's colorless-concurrency stance). The surface foundation
   is **already in place**: `@import("iface","name") async function
   dep(): i32;` parses today (the `async` contextual modifier sets
   `FuncDecl.Async`; `@import` sets `ImportIface`) — verified, no parser
   change needed. Only the caller needs to be `async`-lifted (it must
   own a task to await), which the `async function` keyword already
   provides.

### Codegen plan for the colorless async import (the remaining vertical)

A tightly-coupled but precedented vertical (mirrors the existing
composite-result `@import` **extern-wrapper** machinery,
`scanExternImports`/`externWrappers`):

- **wasmbin**: an `async @import dep(): i32` emits its raw core import
  with the **async-lower core signature** `(retptr) -> status` (not a
  direct `() -> i32`), plus a generated wrapper `__async_dep` that
  allocs an 8-byte return area, calls the raw import, (sync case) reads
  the result from the return area and returns it — exactly the
  extern-wrapper pattern, so a Fern `dep()` call resolves to the wrapper
  and stays colorless. The enclosing function must be `async`-lifted.
- **composer**: classify the async import (from `FuncDecl.Async` +
  `ImportIface`) and lower it with `canon lower async` + the user
  module's memory via the existing `gMem` **trampoline** (solves the
  lower-memory circularity for a single-memory program — the proven
  `PutCanonSectionLowerAsync` over the trampoline memory).
- **test**: compile such a program, compose it against a nested async
  provider (the proven `PutComponentSection` path), run under the async
  features. Expected: `dep()`'s value flows through colorlessly.

The ABI, the emitters, and the nested-provider composition are all
proven (TestWasmP3AsyncImportAwait); this vertical wires them to Fern
source. It is the next focused build.

**STATUS — DONE for the scalar case: Fern source → runnable colorless
async import.** Both halves landed together (coupled-for-correctness):

- **wasmbin** (`scanExternImports`, gated on `ir.ExternFunc.Async`): an
  `@import(...) async function dep(): i32` emits its raw core import with
  the `canon lower async` signature `(scalar params…, retptr) -> i32
  status` (`dep$import`), plus a generated wrapper the Fern `dep()` call
  resolves to — it allocates an 8-byte-aligned return area, calls the raw
  import, and (sync completion) drops the status and reads the result
  inline, so the await stays colorless (no `await` keyword). Scalar
  params + scalar result this slice; a string/array/composite async param
  or result is rejected with a clear error. Helper:
  `buildExternAsyncScalarResultWrapper`. Tests:
  `TestScanExternImportsAsyncScalar` / `…SyncStaysDirect` /
  `…RejectsMemParam` (`internal/codegen/wasmbin/async_extern_test.go`).
- **composer** (`component.BuildAsyncImportAwaitComponent`): lowers the
  async import with `canon lower async` over the consumer's memory via
  the **P2 `gMem` trampoline** (`TrampolineModuleForParamsResults` /
  `FixupModuleForParamsResults` — the same machinery that breaks the
  lower→memory→instance circularity for sync mem imports), bundles the
  async provider as a NESTED component (so the result is a single
  self-contained component, no host needed for the import), and lifts the
  consumer's async core export with `canon lift async`. Both async-ABI
  directions run together.
- **e2e** (`TestWasmP3AsyncImportFromFern`): real Fern source
  (`@import async function dep(): i32` + colorless `async function run()
  { return dep(); }`) compiles, composes against a bundled provider, and
  returns 42 under `wasmtime -W
  component-model-async,component-model-async-stackful --invoke 'run()'`.
- **scalar params** (`TestWasmP3AsyncImportParamsFromFern`): an async
  import that takes arguments — `@import async function add(a: i32, b:
  i32): i32` + colorless `run() { return add(40, 2); }` — round-trips
  through the `(a, b, retptr) -> status` lower against a param-taking
  provider (`add: async func(u32, u32) -> u32`, built by
  `BuildAsyncLiftedExportComponentParams`), returning 42. The wasmbin
  wrapper forwards each scalar param ahead of the return-area pointer.

**Still remaining on the import side:** a *pending* (non-synchronous)
host import needs the `waitable-set.wait` await loop (the sync case reads
the result inline); string/array/composite async params + results;
wiring the consumer assembly into the CLI driver (today the runnable
assembly is exercised through `BuildAsyncImportAwaitComponent` directly,
as the bring-your-own async provider has no CLI surface yet).

**Also remaining:** `future<T>` / `stream<T>` parameter+result lowering;
wiring async into the `concurrent { … }` desugar as an alternative to
the P2 pollable reactor.

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
