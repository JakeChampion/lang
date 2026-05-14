# SSO Two-Word ABI Atomic Flip — In Progress (Draft)

Companion to `docs/SSO-TWOWORD-EXEC.md`. This branch carries
**incomplete** atomic-flip work. **Tests are red.** The doc
exists so the next session opens with a precise read of what's
done, what's broken, and what's left.

## Progress snapshot

Wasm e2e cascade: 67 → 58 → **15 failing** tests after the Map
runtime two-word migration (Option A from the previous session's
notes). Native suite **fully green** (`x86_64` files / HTTP /
streaming all pass — pre-existing SSO-inline / empty-sentinel /
HTTP-handler failures are unrelated to this work and were red
on the prior baseline too). Native parity is held by a
target-aware split — see "Native deferral" below.

## What's done in this branch

### Runtime helpers (§1–§4)

Every wat-side helper with a string param or string return is
flipped to the two-word `(data, len)` ABI:

  - `$__lang_str_len(data, len) → i32` — flag-aware length read.
  - `$__lang_str_byte(data, len, i) → i32` — inline-aware byte
    fetch; splits the index range so bytes 0..3 come from
    `$data` and bytes 4..6 from `$len`.
  - `$__lang_str_to_heap(data, len) → (i32, i32)` — multi-value
    return; inline → fresh heap alloc + copy.
  - `$__lang_str_data_ptr(data, len) → i32` — spills inline at
    mem[0..7] (the scratch collision warning in the previous
    status doc turned out to be benign — preview-2 putchar
    uses constants for its `(ptr=0, len=1)` args, not the
    static iovec at mem[4..11]).
  - `$__str_concat`, `$__str_eq`, `$__str_slice`, `$__str_idx`,
    `$string_from_bytes`, `$__bytes_to_lang_string`,
    `$__method_string_as_bytes`, `$print` / `$write` / `$eprint`,
    `$env`, `$__stream_read_line`, `$tcp_recv` / `$tcp_send`,
    `$__method_Reader_read_chunk`, `$__method_Writer_write`,
    `$args`, `$read_file` / `$write_file`,
    `$open_reader` / `$open_writer` / `$open_appender`,
    `$__build_io_error` (including the IoError variant alloc-
    size shifts for string-payload variants), and the
    `$__http_entry` HTTP wrapper all migrated.
  - `internOrPackMethod(s) → (data, len)` and a new
    `internStringTwoWord(s) → (data, len)` for runtime sites
    that need to embed a static string literal.
  - `__str_idx` split from byte-array stride-1 indexing: new
    `__arr_idx_1` helper in all three backends (wasm + arm64 +
    x86_64) means byte-array `s[i]` no longer accidentally
    triggers the SSO inline-spill dispatch.

### Wat emit-side helpers (§5)

  - `emitStrLenFromLocal(local)` pushes `$<local>_data` +
    `$<local>_len` then calls `$__lang_str_len`.
  - `emitStreamsWriteString(handle, local)` pushes the pair
    twice (once each for `$__lang_str_data_ptr` and
    `$__lang_str_len`).
  - `emitPromoteStrParam(local)` reads + writes back the
    `_data` / `_len` pair.
  - `emitInlineOutputBuild` / `emitHeapStrAlloc` /
    `emitStrEmpty` all flipped already.

### Function signatures + locals (§6)

  - New `watTypes(t) → []string` returns `["i32", "i32"]` for
    `ast.StringType`, `[watType(t)]` otherwise. Used by every
    `(param …)` / `(result …)` / `(local …)` emit site so a
    string-typed slot named `s` materialises as two wasm locals
    `$s_data` / `$s_len`.
  - New `slotNames(base, t)` pairs with `watTypes` to produce
    the matching local names.
  - User-function emission (`wasm_ir.go` function header +
    `(local …)` decls + scratch decls) all fan out for string
    types.
  - `OpLoadLocal` / `OpStoreLocal` / `OpTeeLocal` fan their wat
    emission into two `local.get` / `local.set` ops when the
    slot is string-typed. Store/Tee pop `_len` first then
    `_data` to match the (data, len) operand-stack convention.
  - Implicit return-value padding for non-explicit-return
    string-returning functions pushes `(0, 0)`.

### IR-level memory ops (§7)

  - New `WidthString = -2` sentinel on `Op.Width`.
  - `payloadStoreOpFor(t, ptrW)` / `payloadLoadOpFor(t, ptrW)` /
    `arrayElemStoreOpFor(t, ptrW)` return `Width: WidthString`
    only on wasm32 (ptrW=4). Natives keep `WidthPtr` for now.
  - `OpLoad` / `OpStore` with `Width == WidthString` fan out to
    two `i32.load` / `i32.store` ops at offsets +0 / +4, threaded
    through three scratch locals `$__str_pair_addr` / `_data` /
    `_len`. `containsStringPairMem` gates the local declarations.
  - `payloadStoreOpFor` / `payloadLoadOpFor` / `arrayElemStoreOpFor`
    are now called from all 19 IR-builder call sites with
    `b.ptrW` threaded in.
  - `Program.PtrW` recorded by `LowerWith` so post-Lower passes
    (Inline / FlattenBranches) can stay ptrW-aware.
  - New `BlockTypeStringPair = 5` sentinel (wat-emitted as
    `(result i32 i32)`). Inliner's wrapper block + the branch-
    flattener both pick this for string-returning callees on
    wasm32. `*ast.IfExpr` lowering also flips its block type
    when the if-expression's static type is string.
  - `ElemSizeBytesFor(StringType, ptrW)` returns `2 * ptrW = 8`
    on wasm so `string[]` array stride matches the two-word ABI.
    Natives stay at `ptrW = 8` (same byte count, different
    reason — single LSB-tagged pointer slot).
  - `stringSlotSize(ptrW)` is target-aware: returns `2 * ptrW`
    on wasm32, `ptrW` on natives. Both end up at 8 bytes per
    slot today.

### HTTP wrapper (§8)

  - `$__http_entry`'s method/path/body locals split into
    `_data` / `_len` pairs.
  - 9-arm method dispatch sets both halves per arm; the
    fallback `other(s)` path consumes the (data, len) pair
    returned by `$__bytes_to_lang_string`.
  - `path_with_query`'s `if (result i32 i32)` returns both
    values from each branch.
  - HttpRequest struct alloc grew from 12 → 24 bytes; layout
    is (method @+0/+4, path @+8/+12, body @+16/+20).
  - HttpResponse read at +0 (status), +8 (body data), +12
    (body len) — 8-byte alignment shoulder skips bytes 4..7.

### Native deferral (§9)

Native backends (`arm64`, `x86_64`) intentionally **NOT**
migrated to the two-word ABI yet. Two mechanisms keep them
green while wasm flips:

  - `stringSlotSize(ptrW)` returns `ptrW = 8` on natives (one
    LSB-tagged pointer slot), `2 * ptrW = 8` on wasm32 (two
    `(data, len)` slots). Same byte count, different shape —
    so layout offsets in struct fields / variant payloads /
    array elements stay the same on both targets while only
    wasm's operand-stack arity changes.
  - `payloadStoreOpFor` / `payloadLoadOpFor` /
    `arrayElemStoreOpFor` return `Width: WidthString` only when
    `ptrW == 4`; natives get `Width: WidthPtr` (one ptr-width
    store/load).
  - `returnBlockTypeFor` / `IfExpr` block-type dispatch only
    pick `BlockTypeStringPair` when `b.ptrW == 4`.
  - Per-backend `emitInlineIdxHelper` got an `__arr_idx_1` arm
    (same body as the existing `__str_idx` minus the inline-
    flag check) so the IR's renamed byte-array stride-1 call
    site links on natives.

Net result: native struct field offsets, variant payload
offsets, function signatures, IR ops — all unchanged in
behaviour. The full native e2e suite (HTTP / files / streams /
reader-writer) passes.

## What's broken — 15 wasm e2e failures, mostly closure-capture / state / cascades

The Map runtime migration cleared **43 of the 58** prior
failures. The remaining 15 are predominantly closure-capture,
state globals, and downstream cascade. The Map work
documented in section §11 below describes how it was done.

### 0. (was §1) Map runtime two-word migration — DONE in this session

Done via "cell-pointer boxing" at the call boundary. Same
pattern the wide-V (`i64` / `u64` / `f64`) helpers already
use. The prelude's i32-shaped `(m, k: i32, v: i32)` signatures
stay; on wasm32 only, the IR boxes a string K or V at the call
site into a fresh 8-byte cell holding `(data, len)`, then passes
the cell pointer through. The prelude reads it back via the
`(entryK as string) == (k as string)` pattern, which now picks
up an automatic cell-deref via the cast-lowering change below.

Concrete IR changes:

  - **Cast lowering** (`internal/ir/ir.go`, CastExpr handler):
    `(i32 | usize) as string` on wasm32 (`b.ptrW == 4`) now
    emits `OpLoad{Width: WidthString}`, fanning the single i32
    cell pointer into a `(data, len)` pair via two `i32.load`s
    at offsets +0 / +4. On natives the cast remains a no-op
    (single ptr-shaped string slot).
  - **Boxing dispatch** (`callBody`): the existing wide-V
    dispatch (`isWideScalar`) extends to string K (always boxed
    on wasm32) and string V (boxed on wasm32). Three helpers
    cover the matrix: `emitWideMapSet` (K+V box), `emitWideMapGet`
    (K box + V unbox into Option[string]), `emitWideMapGetOr`
    (K+V box + V unbox), plus `emitStringKMapCall` for methods
    whose return shape passes through (has / delete / get when
    V is scalar).
  - **MapIter / `MapIter_key` / `MapIter_value`**: cell-pointer
    return unboxes via `payloadLoadOpFor` (which already returns
    `OpLoad{WidthString}` on wasm32 for string types).
  - **`Map { … }` literal lowering**: the per-entry alloc-and-
    store path now boxes K and V uniformly through
    `pushMapMethodArg` (renamed from the wide-only path).
  - **PropagateCopies**: a dead `OpStoreLocal` on a two-word
    string slot is replaced with `OpDrop{Width: WidthString}`
    so the wasm codegen fans the drop to two `drop`
    instructions, balancing the two stack values the original
    store would have consumed. Without this the dead-store
    rewrite imbalances every string-typed local that's stored
    but never read (frequent in the prelude after inlining).
  - **ExprStmt drop fan-out**: an `Assign` used as an
    expression-statement leaves the assigned value on the
    operand stack (the assign-as-tee pattern). For string
    locals on wasm32 that's two values; the ExprStmt's
    discard now emits `OpDrop{Width: WidthString}` (and
    `exprType` recognises `*ast.Assign` so the drop sees the
    right type).
  - **Wasm codegen `OpDrop`**: `Width: WidthString` fans out
    to two `drop`s; default (i32 / scalar) emits one.

The prelude itself stays unchanged — no map-runtime rewrite
needed because the cast-lowering change makes the existing
`(entryK as string) == (k as string)` work cleanly under the
two-word ABI (it now loads `(data, len)` from the entries
array's cell pointer, then `OpStrEq` consumes the four
operands it expects).

The remaining wasm e2e failures fall into the categories below.

### 1. Closure captures store string values as single ptr-width slots

`OpMakeClosure` / `OpMakeEnv` build an env cell that's a
pointer-aligned record of captured locals. For each string
capture today the env reserves `ptrW` bytes; loading via
`OpCaptureLoad` produces a single i32. The two-word ABI
needs the env cell to reserve 2 ptrW slots per string capture,
and `OpCaptureLoad` to emit two `i32.load`s.

Touches `internal/ir/closureconv` + the wasm
`OpCaptureLoad` / `OpCaptureStore` handlers (it's a small set
of edits, but the layout decision needs to be made carefully so
arm64 stays one-slot per pointer).

### 3. State globals carrying strings

Same shape as closures: state-var slots reserve `ptrW` for
each declared field. String-typed state vars need 2 slots on
wasm. Likely a 5-line edit in the state-init / state-load /
state-store path once the closure-capture layout pattern is
settled.

### Other minor leftovers

  - `TestWASMFStringInterpolation` — likely a producer arity
    mismatch in one of the f-string helper call sites; needs a
    focused look at the f-string lowering.
  - 6 pinned wasm tests that deliberately assert the **old**
    one-i32 string shape (TestStringFieldOffsetIsPointerWidth /
    TestStringParamIsSingleI32Slot / TestOpConstStrShortLiteral
    PacksInline / TestOpConstStrLongLiteralStaysHeap /
    TestStringsLowerToLinearMemory /
    TestHttpWrapperShortMethodPacksInline) need their golden
    output updated for the post-flip shape, or removed
    entirely. §10 cleanup.

## What's left, in execution order

1. **Closure captures + state globals string layout** — single
   PR; touches `internal/closureconv` + IR `OpCaptureLoad` /
   `OpCaptureStore` handlers + the wasm side. Likely clears
   `TestWASMClosureCapturesString` / `TestWASMClosureCapturesMixedPointers`
   / `TestWASMStateStringConcat` / `TestWASMStateMixedInit` and
   probably the streaming / file-I/O tests too (they go through
   closures internally for the defer-cleanup path).
2. **Generic function instantiation + tuple destructure** —
   `TestWASMGenericFunctionMultipleInstantiations`,
   `TestWASMGenericResult`, `TestWASMTupleDestructure`. Likely
   missing fan-out at some IR pass that doesn't see the
   substituted type. Worth a focused investigation.
3. **Pin-test refresh** — update the 6 pin tests to assert the
   post-flip shape, plus the `LengthWasm` bug fix called out
   in the earlier status doc (the helper masks bits 0..30 when
   it should dispatch on `IsInlineWasm`). §10 cleanup.
4. **Native flip in lockstep (§9, optional)** — arm64 +
   x86_64 backends pick up the two-word ABI per
   `docs/SSO-TWOWORD-EXEC.md`. Once natives flip,
   `stringSlotSize` / `payloadStoreOpFor` / `payloadLoadOpFor` /
   `returnBlockTypeFor` collapse back to ptrW-agnostic forms.

## Session split estimate (updated)

| Session | Scope                                                     |
|---------|-----------------------------------------------------------|
| done    | §1–§8 (helpers + watTypes + WidthString + HTTP wrapper)   |
| done    | Map runtime two-word migration via cell-pointer boxing    |
| next    | Closure-capture + state-global string layout              |
| then    | Generic instantiation + tuple destructure + pin-test refresh |
| later   | §9 native flip (arm64 + x86_64 in lockstep, or staged)    |
| final   | §10 cleanup (drop PackTinyWasm / TinyInlineCapWasm /
            data-segment length-prefix dead bytes)            |
