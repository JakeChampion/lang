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

### `for x in stream` — works EAGERLY, by construction; not colored-lazy

`for x in body() { … }` over a stream-returning call **already works**, for free
and consistently with the colorless model: the checker rewrites the `stream[T]`
result to `T[]` (the eager collected array), so the ordinary parse-time array
`for-in` desugar (`.len()` + index) iterates it after the collect-wrapper drains
the stream to EOF — no intermediate `var b: u8[] = …` and no for-in rework
needed. Locked by `internal/e2e` `TestWasmP3StreamForIn` (→ 42).

True *element-at-a-time* lazy iteration (process each item as it arrives off the
wire, before EOF) is the *colored* model — each loop step an implicit await. It is
being added behind the `for-in` node centralization (below); until L2 lands,
`for x in stream` stays **eager collect-then-iterate** (locked by
`TestWasmP3StreamForIn`).

### Lazy `for x in stream` — DONE (L1 + L2 + general-T)

`for x in body()` over an async stream import iterates LAZILY — one element
pulled off the wire (read + await) per loop turn, never materialising the whole
sequence — for **any scalar element type** (`u8` / `i32` /
`u32` / `i64` / `u64` / `f32` / `f64`). The vertical:

- *Parser* (`desugarForEachProgram`): builds the set of module-local async stream
  imports whose element is a **scalar** (`ast.StreamElemKind != ""`) and **leaves**
  a `for x in f(args)` over one as an `ast.ForEach`; every other iterand — arrays,
  strings, and non-scalar (string/struct/enum) streams — still lowers eagerly via
  `ast.DesugarForEachArray`. `isLazyStreamIter` is the test.
- *Checker* (`lowerStreamForEachProgram`, after the slice-2 stream rewrite so
  `StreamResultElem` is set, and after modload mangling so `f$open` tracks the
  mangled import name): lowers each surviving stream `ast.ForEach` to the cursor
  loop via `ast.DesugarForEachStream`, and registers the helper `FuncSig`s
  (`f$open: (params) -> i32`, `__stream_next: (i32) -> i32`, `__stream_drop: (i32)
  -> ()`, and per element kind `__stream_elem_<kind>: (i32) -> T`).
- *wasmbin* (`extern.go` + scan in `wasi.go`): a lazy stream is driven through a
  small heap **cursor** (`{ rd:i32 @0, elem:8B @8, evt:8B @16 }`):
  `buildExternAsyncStreamOpenWrapper` (`f$open` — collect-wrapper prologue, stores
  the readable handle in a fresh cursor, returns it), `buildStreamNext`
  (element-type-agnostic: `stream.read` ONE element into `cur.elem` + await,
  returns 1/0 for read/EOF — the read count is in *elements*, so stride is
  implicit in the component's `stream<T>` type), `buildStreamElem` (per-kind
  width-/sign-/float-aware load of `cur.elem`), `buildStreamDropReadable`
  (`drop-readable(cur.rd)`). The scan emits `f$open` + the two generic helpers +
  this import's `__stream_elem_<kind>` when the `$open` companion is used (gated by
  `usedOpen`); the eager collect-wrapper still emits under `f` for value context.
  `build.go` pins async stream imports past tree-shake (reached only via `$open`).
- *Composer + e2e*: `BuildAsyncStreamImportComponent` reused unchanged;
  `TestWasmP3StreamForIn` (u8) and `TestWasmP3StreamI32ForIn` (i32) exercise the
  lazy path → 42, with the desugar structure pinned by
  `checker.TestStreamForEachDesugarsToLazyLoop`.

Separating the EOF flag (`__stream_next` → 1/0) from the value read
(`__stream_elem_<kind>`) is what makes this work for ANY scalar — unlike a single
`i32` with a `-1` EOF sentinel, which was unambiguous only for `u8`. Non-scalar
(string / struct / enum) element streams still collect eagerly (their lazy form
needs RC-aware per-element handling — a follow-on). Below is the original design
(kept for the mechanics).

### Lazy `for x in stream` — design (L1 done, L2 pending)

**L1 — DONE** (#3963): the plain `for x in expr` form parses to `ast.ForEach` and
is lowered in one place (`ast.DesugarForEachArray`) at the end of parsing, so a
single decl-aware step can choose the lowering. Behavior-preserving; every
pre-checker statement-walker was taught the node (incl. the `Array.build`
builder-threading desugar).

**L2 — the lazy codegen** (the remaining vertical, must land atomically — it
flips the eager semantics). Concretised to a near-mechanical plan (this is the
architecture validated by attempt; codegen lowers via `internal/ir`, so DON'T
route ForEach through IR — instead the checker desugars the stream ForEach to a
normal Fern loop calling codegen helpers, so IR/codegen only ever see ordinary
constructs):

- *Parser* (done in attempt, ~30 LOC): `desugarForEachProgram` builds a set of
  module-local `@import async … : stream[T]` names; a `for x in f(args)` over one
  is **left as `ast.ForEach`** (the array case still lowers via
  `ast.DesugarForEachArray`). modload/constfold/walk already traverse ForEach
  (L1), so it reaches the checker.
- *Checker* (~60 LOC): a pre-pass after the slice-2 stream rewrite (so
  `StreamResultElem` is set) desugars each stream `ast.ForEach` into a Fern block,
  and registers the helper FuncSigs. For `for x in body()` with `stream[u8]`:
  ```
  var __h: i32 = body$open();              // per-import: raw lower → readable handle
  while (true) {
      var __v: i32 = __stream_next_u8(__h); // read 1 + await; element (0..255) or -1 at EOF
      if (__v < 0) { break; }
      var x: u8 = __v as u8;
      BODY                                  // real loop ⇒ break/continue/return work
  }
  __stream_drop(__h);
  ```
  Register sigs: `body$open: () -> i32` (per stream import, at the slice-2 site),
  `__stream_next_u8: (i32) -> i32`, `__stream_drop: (i32) -> ()`. The `i32`
  sentinel (`-1 = EOF`) avoids constructing `Option[T]` in wasm — fine for `u8`
  (the MVP element type); a general-`T` form later uses a 2-word `(ok, elem)` or an
  Option. `body()` is NOT collect-rewritten here (the call is replaced by
  `body$open`).
- *wasmbin* (~90 LOC; `extern.go` + scan in `wasi.go`): three helpers, all
  adapted from `buildExternAsyncStreamResultWrapper`:
  - `body$open` (per stream import): the collect-wrapper's prologue only — alloc
    retarea, call `body$import`, `emitAsyncAwaitLoop`, return `load(rb+0)` (the
    readable handle).
  - `__stream_next_u8` (generic): `ws.new`+`join(h,ws)`; `stream.read(h, buf, 1)`;
    if `0xffffffff`, `waitable-set.wait` + take `evt+4`; `join(h,0)`+`ws.drop`;
    `count=rs>>4` ≥1 → return `load8_u(buf)`, else `-1`. (Needs `InstElse` +
    `BlocktypeI32`.)
  - `__stream_drop` (generic): `stream.drop-readable(h)`.
  Scan: a `stream[T]` import still emits the collect-wrapper for value context AND
  `body$open`; register `__stream_next_u8`/`__stream_drop` once; reuse the
  `stream.read`/`drop-readable`/waitable intrinsics.
- *Composer + e2e*: **reuse `BuildAsyncStreamImportComponent` unchanged** — it
  already provisions `body$import` (dep-lower), `stream.read`, the waitable set,
  and `stream.drop-readable`, which is exactly what `body$open`/`__stream_next_u8`/
  `__stream_drop` consume. e2e: real Fern `for x in body() { sum += x as i32 }`
  against the EOF producer → 42.

Per-element mechanics are the already-validated collect-loop body (`scollect2.wat`)
— no new derivation. Ships when parser + checker + wasmbin + e2e are green
together (no half-step, since it flips the working eager behavior).

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
   - **Status packing — PINNED** (runnable spike, wasmtime 37): a not-ready read
     returns the **BLOCKED sentinel `0xffffffff`** (the count is NOT in the read
     return). The completion arrives via the event: `waitable.join(rd, ws)` (the
     **readable handle itself** is the waitable — there is no subtask handle) →
     `waitable-set.wait(ws, evtptr)` returns event-code **`2` = STREAM_READ** and
     writes `[waitable, result]` at `evtptr` (`evtptr+0` = the readable handle,
     `evtptr+4` = the **result**). The result packs `(count << 4) | code` with
     **`code`: `0` = COMPLETED (more may come), `1` = CLOSED (EOF)**, `2` =
     CANCELLED; `count = result >> 4`. A synchronously-ready read returns that
     same `(count<<4)|code` directly (when it is not `0xffffffff`).
   - **Collect loop (pinned shape):** `ws = waitable-set.new(); waitable.join(rd,
     ws); loop { rs = stream.read(rd, buf, cap); if rs == 0xffffffff { wait(ws,
     evt); rs = load(evt+4) }; count = rs>>4; copy count elems buf→array;
     if (rs & 0xf) != 0 break }` then **`stream.drop-readable(rd)` BEFORE
     `waitable-set.drop(ws)`** (dropping the set while the readable is still a
     child traps `resource has children`).
   - Because the read blocks-until-awaited, deriving the status was entangled with
     the await loop — there is no shortcut single-read observation (a second
     un-awaited read on the in-flight handle traps `got Busy`).
   - **Producer (EOF) side is symmetric and also await-driven** (spike finding):
     a producer cannot `stream.drop-writable` while a `stream.write` is still
     in-flight — it traps `cannot drop busy stream`. With no reader yet, the
     write BLOCKs, so the producer must AWAIT its write (`write` → `0xffffffff`
     → `join(wr, ws)` + `wait` → completes when the consumer reads) and only then
     `stream.drop-writable(wr)` to signal EOF. So the runnable stream vertical is
     two-sided: the consumer collect-loop above + a producer that write-awaits
     then drops. For the e2e (slice 4), the bundled producer scaffolding gets
     this write-await; the consumer collect-wrapper (slice 3, the wasmbin
     deliverable) is the read side.
4. **e2e.** Real Fern `@import async function body(): stream[u8]` +
   `var b: u8[] = body();` collect, composed against the stream producer, run
   under wasmtime → the collected bytes (mirrors `TestWasmP3StreamExportImport`,
   from Fern source). The runnable payoff.

## `stream[T]` as an async-import PARAMETER (the produce side)

The mirror of the result side: an `@import async function sink(s: stream[u8])`
accepts an eager `u8[]` and the wrapper streams its elements OUT over the wire
(useful exactly as a `stream[u8]` result collects an eager `u8[]` from a
streaming wire — the wire is incremental, the Fern surface is eager).

- **P1 — DONE** (#3934): checker rewrites a `stream[T]` param to `T[]` and records
  `FuncDecl.StreamParamElems[i]` → `ir.ExternFunc.StreamParamElems`; wasmbin guards
  it pending the produce-wrapper.
- **P2 mechanics — PINNED & runnable** (wasmtime-37 spike → 42). The produce
  wrapper, ported from the validated template:
  - `(rd, wr) = stream.new()` (readable=low32 / writable=high32);
  - `status = sink_lower(rd, retptr)` — pass the **readable** handle as the
    canonical param (so the host gets the read end), retptr for the scalar result;
  - **write-stream the eager array** to `wr` (write-await): `ws =
    waitable-set.new(); waitable.join(wr, ws); loop { rs = stream.write(wr, ptr +
    wrote*stride, count - wrote); if rs == 0xffffffff { wait(ws, evt); rs =
    load(evt+4) }; wrote += rs>>4; if wrote >= count || (rs & 0xf) != 0 break }`
    (event-code for a write completion is `3` = STREAM_WRITE; same
    `(count<<4)|code` packing);
  - `waitable.join(wr, 0)` (unjoin) → `stream.drop-writable(wr)` (EOF) →
    `waitable-set.drop(ws)`;
  - **await the lowered call** (the host sink subtask) via the normal
    `emitAsyncAwaitLoop` (status low-nibble != RETURNED(2) → subtask = status>>4,
    join+wait+`subtask.drop`), then read the scalar result at retptr.
  - The host scaffolding is a provider `sink: async func(s: stream[u8]) -> u32`
    whose core collect-reads the param (the read loop above) and task-returns the
    sum.
  - Remaining P2 build: the wasmbin produce-wrapper (`buildExternAsyncStreamParamWrapper`)
    + intrinsics (`stream.new`, `stream.write`, `stream.drop-writable`) +
    composer provisioning (stream.write trampolined over the consumer memory) +
    the host sink provider + e2e — a direct mirror of slices 3a/3b/4.

## Status — result side COMPLETE; param side started

The `stream[T]` Fern *result* surface is shipped end to end; a `stream[T]` flows
from a host export into Fern source as a colorlessly-collected `T[]`. The
*parameter* surface (produce side) is started: P1 (checker) merged, P2 mechanics
pinned + runnable (above), P2 codegen/composer/e2e remaining.

- Channels at the ABI/composer level: **DONE** (see
  `docs/WASI-PREVIEW3-ASYNC-PLAN.md`).
- `stream[T]` Fern surface — all slices **DONE**:
  - slice 1 (`ast.StreamType` + parser, #3916),
  - slice 2 (checker colorless `: stream[T]` → `T[]` transform + guard, #3920),
  - slice 3a (wasmbin `stream.read`+await collect-wrapper, #3926),
  - slice 3b (runnable composer collect proof → 42, #3929),
  - slice 4 (real-Fern from-source e2e → 42, #3931:
    `TestWasmP3StreamImportFromFern`).
- `future[T]` Fern surface: **intentionally not exposed** (colorless auto-await
  subsumes a single deferred value).

Done since: `stream[T]` **parameters** (produce side from Fern source — P1/P2,
`TestWasmP3StreamParamFromFern`); `u8`+`i32` **stride** coverage in both
directions; `for x in stream` **eager** iteration (`TestWasmP3StreamForIn`); and
the **CLI auto-bundle** — `fern -target wasm -emit core-module -async-provider PATH` (or
`-async-provider WITNAME=PATH`, repeatable) bundles bring-your-own provider
component(s) so async `@import`s (scalar params + result, **single or multiple**)
yield one self-contained runnable component (`cmd/fern` `TestAsyncProviderBundle*`,
via `BuildAsyncImportsAwaitComponent`); plus `-async-export` lifting param'd async
functions (`TestAsyncExportParamsCLI`).

Done: **lazy `for x in stream`** — L1 (the `ast.ForEach` centralization, #3963),
L2 (the lazy codegen + atomic eager→lazy flip for u8 streams), and the **general-T**
generalisation (any scalar element via the cursor model: `f$open` /
`__stream_next` / `__stream_elem_<kind>` / `__stream_drop`); see the "DONE
(L1 + L2 + general-T)" section above. Follow-on: lazy iteration of non-scalar
(string / struct / enum) element streams (needs RC-aware per-element handling).

**Possible follow-ons (new design efforts, not gaps in this surface):**
widening the CLI auto-bundle to **stream-result/param** providers (the stream
composers currently take hand-built cores rather than a bring-your-own provider
component) and to string/list-shaped async imports.
