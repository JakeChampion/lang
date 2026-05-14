# SSO Two-Word ABI Atomic Flip — In Progress (Draft)

Companion to `docs/SSO-TWOWORD-EXEC.md`. This branch carries
**incomplete** atomic-flip work. **Tests are red.** The doc
exists so the next session opens with a precise read of what's
done, what's broken, and what's left.

## Progress snapshot

Wasm e2e cascade: 67 → **58 failing** tests, native suite
**fully green** (`x86_64` files / HTTP / streaming all pass).
Native parity is held by a target-aware split — see "Native
deferral" below.

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

## What's broken — 58 wasm e2e failures, mostly Map / closure-capture

Two architectural shapes account for the bulk of remaining
failures. Both are deferred because they need fresh design
decisions, not mechanical edits.

### 1. Map runtime stores string keys as single ptr-width slots

`__map_set_impl` / `__map_get_impl` / `__map_delete_impl` /
`__map_iter_*` in `internal/prelude/prelude.lang` load each
entry's key with `__load_ptr(entriesBase + b * entryStride)`
and store with `__store_ptr(...)`. For `Map[string, …]`, the
single-i32 result is then cast to string: `(entryK as string)
== (k as string)`.

The cast is a no-op at the AST level, but the IR for
`OpStrEq` expects 4 operands on the stack: `(a_data, a_len,
b_data, b_len)`. Today's prelude pushes 2. The wasm validator
rejects the module — `expected i32 but nothing on stack`.

The proper fix needs the map runtime to know whether keys are
`string` (2 ptrW slots) or scalar (1 ptrW slot). Options:

  - **Option A**: keyKind-aware entry stride — `entryStride` is
    already a runtime field; bump it by `ptrW` when keyKind=1.
    Then `__load_ptr` becomes two loads for string keys; same
    for store. Touches every key-touching line in the four
    map ops; doable but a deeper prelude rewrite.
  - **Option B**: introduce `__load_string(addr) → (data, len)`
    / `__store_string(addr, data, len)` wat shims and use them
    in the string-key branches of the map prelude. Pairs with
    Option A's entry-stride bump.

Either way, the map runtime change is a self-contained
follow-up (single prelude file + the matching wat helpers).
**~20 of the 58 failures should clear once Map is migrated.**

### 2. Closure captures store string values as single ptr-width slots

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

1. **Map runtime migration** — single PR; touches
   `internal/prelude/prelude.lang` (the four `__map_*` helpers)
   + add `__load_string` / `__store_string` wat shims to
   `internal/codegen/wasm/wasm.go`. Clears ~20 failures.
2. **Closure captures + state globals string layout** — second
   PR; touches `internal/closureconv` + IR `OpCaptureLoad` /
   `OpCaptureStore` handlers + the wasm side. Clears another
   ~10 failures.
3. **F-string interp investigation** — single focused look,
   probably a couple of lines. Clears the remaining miscellaneous
   string-handling failures.
4. **Pin-test refresh** — update the 6 pin tests to assert the
   post-flip shape, plus the `LengthWasm` bug fix called out
   in the earlier status doc (the helper masks bits 0..30 when
   it should dispatch on `IsInlineWasm`). §10 cleanup.
5. **Native flip in lockstep (§9, optional)** — arm64 +
   x86_64 backends pick up the two-word ABI per
   `docs/SSO-TWOWORD-EXEC.md`. Once natives flip,
   `stringSlotSize` / `payloadStoreOpFor` / `payloadLoadOpFor` /
   `returnBlockTypeFor` collapse back to ptrW-agnostic forms.

## Session split estimate (updated)

| Session | Scope                                                     |
|---------|-----------------------------------------------------------|
| done    | §1–§8 (helpers + watTypes + WidthString + HTTP wrapper)   |
| next    | Map runtime two-word migration (Option A or B)            |
| then    | Closure-capture + state-global string layout              |
| then    | F-string interp + 6 pin-test refresh + langstring.LengthWasm fix |
| later   | §9 native flip (arm64 + x86_64 in lockstep, or staged)    |
| final   | §10 cleanup (drop PackTinyWasm / TinyInlineCapWasm /
            data-segment length-prefix dead bytes)            |
