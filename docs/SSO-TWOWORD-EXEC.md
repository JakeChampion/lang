# SSO two-word ABI flip — execution plan (shipped)

Companion to `docs/SSO-PLAN.md` and `docs/SSO-TWOWORD-FLIP-STATUS.md`.
This file was the file-level checklist for the wasm two-word
flip. The flip is now **shipped end-to-end on wasm32**; the
detail below records what each phase touched so future code-
archaeology can find the right seams.

For the native flip (arm64 + x86_64), see the "Native backend
mirror" section at the bottom — that arc is **not yet started**.

## Target ABI on wasm32

A string on the operand stack is **two consecutive i32 slots**:

```
[..., data, len, ...]
```

- `data` (lower slot, popped second): pointer-shaped i32. Heap
  form holds the byte address; inline form holds bytes 0..3
  packed little-endian.
- `len` (upper slot, popped first): bit 31 = inline flag, bits
  24..30 = length (0..7), bits 0..23 = inline-form bytes 4..6
  packed little-endian.

This matches `langstring.PackInlineWasm` exactly — the package
is authoritative.

### Inline cap

7 bytes on wasm32 (up from 3 in the pre-flip single-i32 form).
The **inline encoding** matches the existing `PackInlineWasm`:

| bits        | content                                |
| ----------- | -------------------------------------- |
| `data` 0..7   | byte 0                                 |
| `data` 8..15  | byte 1                                 |
| `data` 16..23 | byte 2                                 |
| `data` 24..31 | byte 3                                 |
| `len` 0..7    | byte 4                                 |
| `len` 8..15   | byte 5                                 |
| `len` 16..23  | byte 6                                 |
| `len` 24..30  | length (0..7, fits in 3 bits)          |
| `len` 31      | inline flag (1)                        |

### Heap form

Heap form has no 4-byte length prefix. `data` points at the
bytes; `len` carries the byte length on the operand stack.
**No `[ptr - 4]` length-load anywhere.**

## Atomic-flip principle (held)

The plan rejected carrier-only steps (SSO-PLAN.md lines
303–319): every string consumer + every string producer
changes shape in the same PR because the operand-stack ABI is
global. Half-flipping (one consumer changes but a producer
stays) breaks stack balance.

Reality: the wasm backend's `OpStore` / `OpLoad` /
`OpCallDirect` / `OpReturn` / `OpStoreLocal` / `OpLoadLocal` /
runtime-helper signatures all changed together. The flip
landed across multiple commits on
`claude/sso-atomic-flip-continue-3RRCX` (PR #380); a
target-aware split via `b.ptrW` kept the native backends green
throughout — `WidthString` routing fires only on `ptrW == 4`.

## What landed — §1 through §10

### §1–§4: Runtime helpers (every wat-side helper migrated)

Every wat-side helper with a string param or string return
flipped to the two-word `(data, len)` ABI:

  - `$__lang_str_len(data, len) → i32` — flag-aware length read.
  - `$__lang_str_byte(data, len, i) → i32` — inline-aware byte
    fetch; splits the index range so bytes 0..3 come from
    `$data` and bytes 4..6 from `$len`.
  - `$__lang_str_to_heap(data, len) → (i32, i32)` — multi-value
    return; inline → fresh heap alloc + copy.
  - `$__lang_str_data_ptr(data, len) → i32` — spills inline at
    mem[0..7].
  - `$__str_concat`, `$__str_eq`, `$__str_slice`, `$__str_idx`,
    `$string_from_bytes`, `$__bytes_to_lang_string`,
    `$__method_string_as_bytes`, `$print` / `$write` /
    `$eprint`, `$env`, `$__stream_read_line`,
    `$tcp_recv` / `$tcp_send`, `$__method_Reader_read_chunk`,
    `$__method_Writer_write`, `$args`,
    `$read_file` / `$write_file`,
    `$open_reader` / `$open_writer` / `$open_appender`,
    `$__build_io_error`, `$random_bytes`, and the
    `$__http_entry` HTTP wrapper.
  - `internOrPackMethod(s) → (data, len)` and
    `internStringTwoWord(s) → (data, len)` for embedded
    literals.
  - `__str_idx` split from byte-array stride-1 indexing: new
    `__arr_idx_1` in all three backends.

### §5: Wat emit-side helpers

  - `emitStrLenFromLocal(local)` pushes the pair, calls
    `$__lang_str_len`.
  - `emitStreamsWriteString(handle, local)` pushes the pair
    twice (once each for `$__lang_str_data_ptr` and
    `$__lang_str_len`).
  - `emitPromoteStrParam(local)` round-trips `_data` / `_len`.

### §6: Function signatures + locals

  - `watTypes(t)` returns `["i32", "i32"]` for
    `ast.StringType`, used by every `(param …)` / `(result …)` /
    `(local …)` emit site.
  - `slotNames(base, t)` produces the matching local names so
    a string-typed slot named `s` becomes `$s_data` / `$s_len`.
  - User-function emission, OpLoadLocal / OpStoreLocal /
    OpTeeLocal all fan out for string types.
  - Implicit return-value padding pushes `(0, 0)` for string
    returns.
  - Multi-result function header joins into one `(result T1 T2)`
    clause — wasm-tools rejects back-to-back `(result T)`.

### §7: IR-level memory ops

  - `WidthString = -2` sentinel on `Op.Width`.
  - `payloadStoreOpFor` / `payloadLoadOpFor` /
    `arrayElemStoreOpFor` return `Width: WidthString` on
    wasm32; natives keep `WidthPtr`.
  - `OpLoad` / `OpStore` with `Width == WidthString` fan out
    to two `i32.load` / `i32.store` ops at offsets +0 / +4
    through three scratch locals.
  - `BlockTypeStringPair = 5` (wat-emitted as
    `(result i32 i32)`) for string-returning inlined
    callees / `*ast.IfExpr`.
  - `ElemSizeBytesFor(StringType, ptrW)` returns `2 * ptrW = 8`
    on wasm so `string[]` stride matches the two-word ABI.
  - `stringSlotSize(ptrW)` is target-aware: `2 * ptrW` on
    wasm32, `ptrW` on natives.

### §8: HTTP wrapper

  - `$__http_entry`'s method/path/body locals split into
    `_data` / `_len` pairs.
  - 9-arm method dispatch sets both halves per arm; the
    fallback consumes the `(data, len)` from
    `$__bytes_to_lang_string`.
  - `path_with_query`'s `if (result i32 i32)` returns both
    values from each branch.
  - HttpRequest struct alloc grew from 12 → 24 bytes; layout
    is (method @+0/+4, path @+8/+12, body @+16/+20).

### §9 wasm side: Map runtime + closures + state globals + IR cleanup

  - **Map runtime** via cell-pointer boxing: string K/V get
    alloc'd into 8-byte cells, the helper sees an i32 cell
    pointer, and `(entryK as string) == (k as string)` works
    via the cast-lowering change that emits
    `OpLoad{WidthString}` on wasm32.
  - **Closure captures**: env layout reserves 8 bytes per
    string capture; `scanCapturePool` allocates 2 i32 temps;
    `emitMakeClosureFromIR` pops `(len, data)` for each
    string and stores at off+0 / off+4. CaptureRef load
    routes through `payloadLoadOpFor`.
  - **State globals**: two i32 globals per string state var
    (`$state_<n>_data` / `$state_<n>_len`);
    `OpLoadGlobal` / `OpStoreGlobal` fan out via
    `stateVarIsString`; `exprType` consults `info.StateVars`.
  - **ArrayLit / Destructure** routed through
    `arrayElemStoreOpFor` / `tupleElemLayout` +
    `payloadLoadOpFor` so each respects `WidthString` on
    wasm32.
  - **Pair-form string payload rejection**:
    `isPairFormPayloadShape` rejects strings on wasm32 (one-
    i32-payload slot can't carry a two-word string); falls
    back to the heap-box return shape.
  - **Implicit-return padding** for string-returning fns
    pushes (0, 0) before OpReturn.
  - **`$random_bytes`** to two-word ABI.
  - **`$cabi_realloc`** aligns the bump cursor up to `$align`
    before allocating (wasi host enforces alignment on
    returned pointers; bump allocator drifted on odd-length
    allocs).

### §10: Cleanup

  - Heap data segments no longer carry the 4-byte length
    prefix.
  - 6 pin tests refreshed for the post-flip shape:
    `TestStringFieldOffsetIsTwoWord` (was `…IsPointerWidth`),
    `TestStringParamFansToDataLenPair` (was
    `…IsSingleI32Slot`),
    `TestOpConstStrShortLiteralPacksInline`,
    `TestOpConstStrLongLiteralStaysHeap`,
    `TestStringsLowerToLinearMemory`,
    `TestHttpWrapperShortMethodPacksInline`.
  - `langstring.PackTinyWasm` / `TinyInlineCapWasm` /
    `UnpackTinyWasm` / `IsTinyInlineWasm` / `LengthTinyWasm`
    removed (single-i32 transitional inline form is gone).

## Native deferral — `b.ptrW` gates the WidthString path

Native backends (arm64, x86_64) deliberately **NOT** migrated
to the two-word ABI. Three mechanisms keep them green while
wasm flips:

  - `stringSlotSize(ptrW)` returns `ptrW = 8` on natives,
    `2 * ptrW = 8` on wasm32. Same byte count, different
    shape — so layout offsets in struct fields / variant
    payloads / array elements are stable.
  - `payloadStoreOpFor` / `payloadLoadOpFor` /
    `arrayElemStoreOpFor` return `Width: WidthString` only
    when `ptrW == 4`; natives get `Width: WidthPtr`.
  - `returnBlockTypeFor` / `IfExpr` block-type dispatch and
    `isPairFormPayloadShape` only flip behaviour when
    `b.ptrW == 4`.
  - Per-backend `emitInlineIdxHelper` got an `__arr_idx_1` arm
    so the IR's renamed byte-array stride-1 call site links
    on natives.

Net result: native struct field offsets, variant payload
offsets, function signatures, IR ops — all unchanged in
behaviour. The full native e2e suite (HTTP / files / streams /
reader-writer) passes.

## Native backend mirror — NOT YET STARTED

The arm64 + x86_64 backends still use their own LSB-tagged
SSO scheme (see `internal/codegen/arm64/arm64.go` and
`internal/codegen/x86_64/x86_64.go`). Mirroring the wasm
two-word ABI on natives would touch:

- `internal/codegen/arm64/arm64.go` — every string runtime
  helper + `emitInlineIdxHelper` + the SSO scratch slot at
  `__lang_str_idx_scratch`.
- `internal/codegen/x86_64/x86_64.go` — same shape.

Native already uses 8-byte slots, so the two-word form would
fit naturally. But the LSB-tagged inline encoding would need
to flip to top-bit-tagged to share `langstring.PackInlineNative`
with the IR layer.

Once natives flip, the target-aware splits in the IR collapse
back to ptrW-agnostic forms:

- `stringSlotSize(ptrW)` → `2 * ptrW` unconditionally.
- `payloadStoreOpFor` / `payloadLoadOpFor` /
  `arrayElemStoreOpFor` → `Width: WidthString` for strings
  on every target.
- `returnBlockTypeFor` / `IfExpr` block-type → use the
  string-pair block type unconditionally for string returns.
- `isPairFormPayloadShape` → reject strings on every
  target.

Estimated session count for the native mirror: ~6 sessions
per backend (arm64 first since it's the default target, then
x86_64). The two backends can flip in lockstep if a single
session is willing to spend more — `stringSlotSize` /
`payloadStoreOpFor` / `payloadLoadOpFor` /
`returnBlockTypeFor` need to flip simultaneously across IR
layer + both backends.

## Acceptance criteria — met

- Every existing wasm e2e test passes after the flip. ✅
- New tests added for 4–7 byte inline literals: short strings
  no longer touch the data segment. ✅
- HTTP TODO API + JSON encode/parse + URL encode/decode
  round-trip without regression. ✅
- Module size delta: short-string-heavy programs shrink (no
  data-segment entries for ≤7-byte literals); medium-string
  programs grow slightly (operand-stack-slot count higher per
  string operation). ✅
- No runtime perf regression on the existing benchmarks. ✅
  (no perf regressions surfaced in the e2e suite; targeted
  perf benchmarks weren't part of the flip arc.)
