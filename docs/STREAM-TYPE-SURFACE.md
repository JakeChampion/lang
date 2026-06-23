# `stream[T]` (and why not `future[T]`) in the Fern language surface

The WASI Preview-3 async **channels** are complete at the composer/ABI level
(`docs/WASI-PREVIEW3-ASYNC-PLAN.md`): byte-verified `future`/`stream` canon
encoders, single-component round-trips, and two-component export/import splits,
all runnable under wasmtime. This note is the design checkpoint for the one
remaining piece — exposing them (or not) in the **Fern language surface**
(parser → checker → IR → wasmbin) — written so the implementation starts from a
decided position. It is the surface-side analogue of the encoder/mechanics
checkpoints that preceded each runnable async slice.

## Decision: expose `stream[T]`, keep `future[T]` internal

Fern's async model is **colorless**: an `@import async function f(): T` is
auto-awaited and the call site yields a plain `T` — there is no `await` keyword
and no "deferred value" surface object (`docs/WASI-PREVIEW3-ASYNC-PLAN.md`).

- **`future[T]` would be incoherent as a surface type.** A `future[T]` is
  precisely "a single value that is not here yet, which you later await" — the
  *colored* concept the colorless model exists to remove. The colorless
  auto-await already subsumes it: a host function that returns a
  `future[T]` on the wire is consumed colorlessly as a `T`. So `future[T]`
  stays an **internal ABI detail** (the lowering already handles a deferred
  single value via the pending-await loop); it is never written in Fern source.

- **`stream[T]` is a genuinely distinct shape** — a *sequence* delivered
  incrementally — so it earns a surface type. The coherent way to deliver it
  under the colorless model is the **same rule, generalised**: where an async
  import declared `: T` yields `T` (the awaited value), an async import declared
  `: stream[T]` yields **`T[]`** at the call site — the fully-collected
  sequence. The `stream[T]` annotation tells the compiler "this result arrives
  incrementally over the wire (drive `stream.read` + the await loop until EOF),
  then hand the caller the materialised array." The user still writes
  straight-line, colorless code:

  ```fern
  @import("wasi:http/types", "body-stream")
  async function body(): stream[u8];

  async function handle(): i32 {
      var bytes: u8[] = body();   // colorless: reads the whole stream into u8[]
      return bytes.len();
  }
  ```

  This is coherent with (a) the colorless model (you get a value, not an
  iterator/await), (b) the existing list-result lowering (a `stream[u8]` result
  materialises as `u8[]` exactly like a `list[u8]` result does — the *only*
  difference is the wire ABI: incremental `stream.read` vs a single `(ptr,len)`
  copy), and (c) Fern's bracket generic syntax (`Pair[T]`, `dyn C[i32]`), so the
  spelling is **`stream[T]`**, not `stream<T>` (angle brackets are not a lexer
  token — see recon §2).

### Why not `for x in stream` (lazy iteration) — for now

The obvious "process elements as they arrive" shape would be lazy iteration
(`for x in body() { … }`). That is the *colored* model (each step is an implicit
await) and, mechanically, `for-in` is desugared at **parse time**, hardcoded to
`.len()` + index access (recon §4). Making a lazy stream `for`-iterable requires
either a fake `.len()`/indexing (semantically wrong → incoherent) or reworking
`for-in` into a **type-driven desugar / Iterator protocol** — a core-semantics
change that deserves its own design pass. The colorless **collect-to-array**
delivery above needs none of that and ships the useful 90% (edge handlers
read a request/body stream to completion before responding). A future
lazy-iteration story can layer on top once an Iterator protocol exists.

## Implementation slices (recon-grounded; file anchors in the recon)

1. **AST + parser foundation.** Add `ast.StreamType{ Elem Type }`
   (`internal/ast/ast.go`: `isType`, `String` → `"stream[" + Elem + "]"`,
   `Equal`, `SubstSelf`, `IsPointerType` → false). Parse `stream[T]` in
   `parser.parseType` (`internal/parser/parser.go` ~L1854, a `stream` keyword
   case consuming `[`, an element type, `]`). Tests: parser round-trip.
2. **Type-system plumbing.** `internal/checker` + `internal/monomorph`
   substitution / equality / reconciliation cases (mirror `ArrayType`, ~5 sites
   each per recon §3/§6). Test: a `stream[T]` annotation type-checks.
3. **Colorless result transform + wasmbin collect-wrapper.** Recognise a
   `stream[T]` result on an async `@import` (`scanExternImports`,
   `internal/codegen/wasmbin/wasi.go`); the call site yields `T[]`. Emit a
   wrapper that drives `stream.read` + the existing pending-await loop, growing a
   length-prefixed Fern array until the stream reports EOF (the
   `BuildStreamExportImportComponent` ABI already provides the read side). Test:
   checker rule.
4. **e2e.** Real Fern `@import async function body(): stream[u8]` + a
   `for`-free collect, composed against the stream producer, run under wasmtime
   → the collected bytes. The runnable payoff (mirrors
   `TestWasmP3StreamExportImport`, but from Fern source).

## Status

- Channels at the ABI/composer level: **DONE** (see
  `docs/WASI-PREVIEW3-ASYNC-PLAN.md`).
- `stream[T]` Fern surface: **designed (this note)**; slices 1–4 pending.
- `future[T]` Fern surface: **intentionally not exposed** (colorless subsumes
  it).
