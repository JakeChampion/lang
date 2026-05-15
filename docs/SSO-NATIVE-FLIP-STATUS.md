# SSO native flip — arm64 first (in progress)

Companion to `docs/SSO-TWOWORD-EXEC.md`. The wasm32 two-word
ABI flip is shipped (PR #382, in main). This doc tracks the
follow-on arc: mirror the two-word ABI on the native
backends, arm64 first then x86_64.

## Target ABI on arm64 (mirrors wasm32 logically)

A string on the operand stack becomes **two consecutive 8-byte
slots** (vs one 8-byte LSB-tagged slot today):

```
[..., data, len, ...]
```

- `data` (lower slot): pointer-shaped i64. Heap form holds
  the byte address; inline form holds bytes 0..7 packed
  little-endian.
- `len` (upper slot): bit 63 = inline flag, bits 56..59 = length
  (0..15), bits 0..55 = inline-form bytes 8..14.

Matches `langstring.PackInlineNative` exactly — the package is
already authoritative.

### Heap form

No length prefix at `[data - 4]`. `data` points at the bytes;
`len` carries the byte length on the operand stack.

### Inline cap

15 bytes on arm64 (up from 7 today). Bytes 0..7 in `data`,
bytes 8..14 in `len`'s low 56 bits, length in bits 56..59
(4 bits, 0..15), flag in bit 63.

The native flip is BOTH an ABI shape change AND an inline-
encoding change (LSB-tagged → top-bit-tagged). Per the
user's call, we flip to top-bit-tagged so `langstring.
PackInlineNative` becomes the shared inline-encoding source
of truth.

## Strategy

Per the previous session's decisions:

- **arm64 first**, x86_64 second. arm64 is the default
  target and runs on Apple Silicon Macs, Graviton, Pi,
  Android — broadest deployment surface.
- **WIP-commits-then-atomic** like the wasm flip: each
  session ships a focused slice; tests can be red on the
  branch during the flip; the final commit lands everything
  green.
- **Flip to top-bit-tagged inline encoding** to share
  `langstring.PackInlineNative` with the IR layer.

## What's done (this session)

### §0. Branch setup + scoping

  - Branched `claude/sso-native-flip-arm64` from main
    (post-wasm-flip merge, head `8f45fa4`).
  - Catalogued arm64 string-handling surface in
    `internal/codegen/arm64/arm64.go`:
    - `emitStrLen` / `emitStrLenStore` / `emitStrDataPtr` /
      `emitStrEmpty` — the SSO seam helpers (4 helpers).
    - `emitStrcatRuntime` / `emitStrcmpRuntime` /
      `emitStrSliceRuntime` / `emitStringFromBytesRuntime` /
      `emitInlineIdxHelper` — the runtime helpers (5 helpers).
    - `OpConstStr` / `OpStrConcat` / `OpStrLen` / `OpStrEq` /
      `OpLoad` / `OpStore` / `OpLoadLocal` / `OpStoreLocal` /
      `OpTeeLocal` / `OpReturn` — the IR op handlers (10 ops).
  - 12 `ptrW == 4` gates in `internal/ir/ir.go` +
    1 in `internal/ast/ast.go` shape the two-word ABI on
    wasm only today. arm64 will flip these to a target-aware
    helper.

### §1. IR-side gate refactor — DONE

`(b *builder) twoWordStrings() bool` added; returns
`b.ptrW == 4` today. The six ad-hoc `b.ptrW == 4` checks
in `internal/ir/ir.go` for string-ABI gates (OpReturn
padding, ExprStmt drop fan-out, cast-to-string load,
*ast.IfExpr block-type, array elem load/store width) all
route through this method now. NO-OP refactor; full test
suite green.

The standalone helpers (`stringSlotSize`, `payloadStoreOpFor`,
`payloadLoadOpFor`, `arrayElemStoreOpFor`,
`isPairFormPayloadShape`, `isStringForBoxing`,
`ast.ElemSizeBytesFor`) continue to gate on their `ptrW`
parameter — they're called from outside the builder
(closureconv, codegen) and flipping their internal gates
is a separate slice once arm64's codegen handles
`WidthString`.

### §1a. arm64 OpLoad / OpStore: WidthString handling — DONE

Added the WidthString case to arm64's `OpLoad` /
`OpStore` handlers. Behaviour:

  - `OpLoad{Width:WidthString}`: pop addr; emit
    `ldr x1, [x0]` (data @ +0) and `ldr x0, [x0, #8]`
    (len @ +8); push both 8-byte slots.
  - `OpStore{Width:WidthString}`: pop len, data, addr;
    emit `str x0, [x2]` (data @ +0) and
    `str x1, [x2, #8]` (len @ +8).

Dead code today — no IR site emits `WidthString` for
natives. Wired here ahead of the arm64 two-word flip so
the eventual gate flip in the IR layer (extending
`twoWordStrings()` to return true on arm64 too) becomes a
one-liner from the codegen-side perspective.

### §1c. Standalone-helper seam: `ast.UseTwoWordStrings` — DONE

Companion to `(b *builder) twoWordStrings()`. Lives in the
`ast` package (not `ir`) so both layers can call it:

  - `ast.ElemSizeBytesFor(StringType, ptrW)` routes its
    `ptrW == 4` check through `ast.UseTwoWordStrings`.
  - `internal/ir/ir.go`'s `useTwoWordStrings(ptrW int)`
    wraps `ast.UseTwoWordStrings` and is consumed by
    `stringSlotSize`, `payloadStoreOpFor`,
    `payloadLoadOpFor`, `arrayElemStoreOpFor`,
    `isPairFormPayloadShape`, `isStringForBoxing`.

The previously-named `(b *builder) twoWordStrings()` also
routes through `ast.UseTwoWordStrings`. Net result: the
"two-word ABI active?" decision lives in exactly one place
(`ast.UseTwoWordStrings(ptrW int) bool { return ptrW == 4 }`)
and the eventual flip for arm64 is a one-line change there.

### §1b. arm64 emit-side helpers (two-word variants) — DONE

Added three Go-level helpers alongside the existing
single-register `emitStrLen` / `emitStrDataPtr` /
`emitStrEmpty`, suffixed `2W` to mark the two-word
variants:

  - `emitStrLen2W(dstW, lenX)` — flag-aware byte-length
    extraction from a `len` register. Top-bit-tagged:
    heap → `lenX_w`, inline → bits 56..59.
  - `emitStrDataPtr2W(dstX, dataX, lenX, scratchOff)` —
    flag-aware linear-memory pointer. Heap → dataX,
    inline → spill 16 bytes to `[x29 + scratchOff]`.
  - `emitStrEmpty2W(dataX, lenX)` — sets the (dataX,
    lenX) pair to the canonical empty-string two-word
    value (data = 0, len = `1 << 63`).

`emitStrLenStore` has no two-word counterpart — heap form
in the two-word ABI carries no length prefix; the helper
is dead-on-arrival under the new ABI. Existing callers
keep using the legacy helper for now.

Dead today; live after the arm64 flip activates. The
`2W` suffix is temporary — once the legacy helpers'
callers all migrate, the `2W` versions take over the
unsuffixed names.

## What's left, in execution order (rough estimate)

1. **§1 IR gate refactor** — rename `ptrW == 4` checks
   to a target-aware helper. NO-OP behaviour change.
2. **§2 Inline encoding flip** — switch arm64's emit-side
   helpers to top-bit-tagged. `emitStrLen` /
   `emitStrLenStore` / `emitStrDataPtr` / `emitStrEmpty`
   all use the new encoding. Inline cap rises from 7 to 15
   bytes (since arm64's inline slot is 8 bytes for `data`
   plus 7 in `len`'s low bytes; with top-bit-tagged, the
   length-nibble + flag move out of `data` and into `len`).
3. **§3 Runtime helpers** — `__lang_strcat`,
   `__lang_strcmp`, `__str_slice`, `string_from_bytes`,
   `__str_idx`, etc. Migrate signatures to `(data, len)`.
4. **§4 IR ops** — `OpStrConcat` / `OpStrEq` / `OpStrLen` /
   `OpStrSlice` / `OpStrIdx` consume / produce two stack
   values. `OpConstStr` emits 2 pushes. `OpReturn` for
   string-returning fns pops 2.
5. **§5 Function signatures + locals** — string params
   become 2 × 8-byte slots in the SysV ABI; string locals
   take 2 frame slots; string returns use the (x0, x1) pair.
6. **§6 OpLoad / OpStore** — fan out `WidthString` to two
   ldp / stp 8-byte pairs (offset +0 / +8).
7. **§7 Closure captures + state globals + struct fields** —
   parallel to wasm's §9; closure env block reserves 2 × 8
   bytes per string capture, state globals split into
   `_data` / `_len` pairs, struct field offsets respect the
   16-byte string slot.
8. **§8 Map runtime + pair-form rejection** — same as wasm:
   strings get cell-pointer-boxed at the call boundary;
   pair-form payload slot can't carry a two-word string.
9. **§9 Cleanup** — drop dead code (LSB-tagged inline
   helpers, length-prefix data-segment bytes, etc.).

## Then: x86_64

Same shape, mirrored. x86_64 uses SysV's `(rax, rdx)` for
two-word return so the ABI lines up cleanly; rdi / rsi / rdx /
rcx / r8 / r9 for args means a string param consumes 2 arg
registers.

## Session split estimate

Per the exec doc: ~6 sessions per backend. This session is
§0 + §1 (foundation + refactor). Future sessions handle
§2–§9.
