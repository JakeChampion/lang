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

### §1. IR-side gate refactor — TBD this session

Plan: introduce `Program.TwoWordStrings` (bool) +
`builder.twoWordStrings()` method. Today they return
`ptrW == 4`; future commits add an arm64 path. This is a
NO-OP refactor — all tests stay green.

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
