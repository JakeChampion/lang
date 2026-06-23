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
   `Equal`, `SubstSelf`; left out of `IsPointerType` — it's transformed to `T[]`
   before codegen, never a materialised value). Parse `stream[T]` **contextually**
   in `parser.parseType`: the existing `Ident[args]` generic-instantiation path
   yields `StreamType` when the name is `stream` with one arg, else `EnumType` —
   no reserved keyword, so `stream` stays a usable identifier. Tests: parser
   round-trip.
   **DONE** (PR #3916): `ast.StreamType` + contextual `stream[T]` parse
   (`Ident[args]` with name `stream`, one arg → `StreamType`, else `EnumType`),
   `TestStreamTypeParse`.

2. **Colorless result transform (early rewrite).** The robust, low-churn way to
   make `body()` yield `T[]` — given the many `fn.ReturnType` readers in the
   checker — is a single **early normalization** (start of `checker.Check`, or a
   tiny pre-pass over `prog.Funcs`, before `FuncSigs` is built at
   `checker.go:2077`): for a func with `ImportIface != "" && Async` and
   `ReturnType` a `StreamType{T}`, set a new `FuncDecl.StreamResultElem = T` and
   rewrite `ReturnType = ArrayType{T}`. After that every checker site (and `ir.go`)
   sees a plain `T[]` async-list result — NO per-site checker changes, NO
   monomorph subst plumbing needed for the result path (slice 1's `Equal`/
   `SubstSelf` cover the rare param/var use). `ir.go` (`ExternFunc` build,
   ~L2608) copies `StreamResultElem` onto a new `ExternFunc.StreamResultElem`.
   Test: checker accepts `@import async function body(): stream[u8]` and types
   `var b: u8[] = body()`.
3. **wasmbin collect-wrapper + composer.** In `scanExternImports`
   (`internal/codegen/wasmbin/wasi.go`), when `ex.StreamResultElem != nil` the
   raw `canon lower async` of the import returns a **stream readable handle**
   (not a `(ptr,len)`); emit a collect-wrapper that drives `stream.read` + the
   existing pending-await loop (`emitAsyncAwaitLoop`), appending each delivered
   chunk into a growing length-prefixed Fern array until the stream reports EOF.
   This needs a `stream.read` (+ `stream.drop-readable`) intrinsic imported under
   `""` (mirror the waitable intrinsics registered for the await loop) and the
   composer (`BuildAsyncImportsAwaitComponent`) to provide them — `stream.read`
   trampolined over the consumer memory (the `BuildStreamExportImportComponent`
   consumer path already proved the read side). This slice is comparable in size
   to the pending-await wiring.

   **Mechanics derived (wasm-tools 1.240 + wasmtime 37, this slice's spike):**
   - canon opcodes: `stream.read 0x0f`, `stream.drop-readable 0x13`,
     `stream.drop-writable 0x14` (core sig of the drops: `(handle) -> ()`).
   - `stream.read` is **async**: a read with no data yet ready returns a
     **BLOCKED** status and leaves the readable end *in-flight* — issuing a
     second read on the same handle traps `invalid handle; got Busy`. So the
     collect loop MUST await each read through the waitable machinery before the
     next: `read(rd, buf, cap)` → if BLOCKED, `waitable.join`+`waitable-set.wait`
     (the existing `emitAsyncAwaitLoop` shape, keyed on the read's subtask) →
     decode the completed count from the read status → append `count` elems to
     the growing array → repeat.
   - EOF is signalled by the producer **dropping the writable end**
     (`stream.drop-writable`); the consumer's next `stream.read` then completes
     with a CLOSED/DROPPED state (low nibble) instead of a data count. The loop
     terminates on that state, then `stream.drop-readable`s its end.
   - Status packing mirrors the async-lower status (low nibble = state/event,
     high bits = count/subtask) — exact CLOSED/COMPLETED nibble values to be
     pinned by the same dump-oracle approach when the loop is built.
   - Because the read blocks-until-awaited, deriving the status is entangled with
     the await loop — i.e. building the collect loop *is* the derivation; there
     is no shortcut single-read observation (a second un-awaited read traps Busy).
4. **e2e.** Real Fern `@import async function body(): stream[u8]` +
   `var b: u8[] = body();` collect, composed against the stream producer, run
   under wasmtime → the collected bytes (mirrors `TestWasmP3StreamExportImport`,
   from Fern source). The runnable payoff.

## Status

- Channels at the ABI/composer level: **DONE** (see
  `docs/WASI-PREVIEW3-ASYNC-PLAN.md`).
- `stream[T]` Fern surface: slice 1 (AST + parser) **DONE** (#3916); slices 2–4
  specified above (slice 3 is the substantial collect-wrapper/composer vertical).
- `future[T]` Fern surface: **intentionally not exposed** (colorless subsumes
  it).
