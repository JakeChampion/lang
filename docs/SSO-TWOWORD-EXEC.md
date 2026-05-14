# SSO two-word ABI flip — execution plan

Companion to `docs/SSO-PLAN.md`. The high-level migration is
already documented (Steps 1–10). This file carries the
**file-level execution checklist** for the wasm two-word flip
so the next session can pick it up and ship it without
re-discovering the surface.

The single-i32 form is shipped (PRs #351–#369). What's left is
the operand-stack ABI flip from one i32 slot per string to two
i32 slots per string.

## Target ABI on wasm32

A string on the operand stack becomes **two consecutive i32
slots**:

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
is already authoritative.

### Inline cap

7 bytes on wasm32 (vs 3 today). The **inline encoding**
matches the existing `PackInlineWasm`:

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

Heap form drops the 4-byte length prefix entirely. `data`
points at the bytes; `len` carries the byte length on the
operand stack. **No `[ptr - 4]` length-load anymore.**

## Atomic-flip principle (re-stated)

The plan rejects carrier-only steps (see SSO-PLAN.md lines
303–319). Translation: every string consumer + every string
producer must change shape **in the same PR** because the
operand-stack ABI is global. Half-flipping (one consumer
changes but a producer stays) breaks stack balance.

Reality check: the wasm backend's `OpStore` / `OpLoad` /
`OpCallDirect` / `OpReturn` / `OpStoreLocal` /
`OpLoadLocal` / runtime-helper signatures all need updating
together. **The flip PR will be large.** That's the cost
the doc warns about; the pre-work below shrinks the surface
each individual file presents inside that PR.

## Pre-work (small, mergeable independently)

These slices land BEFORE the atomic flip. Each is small.
Together they reduce the atomic-flip PR from "rewrite the
backend" to "switch the seams".

### PW-1 — single seam for `(data, len)` push from a local pair

Add `emitStrPushFromLocals(dataLocal, lenLocal)` /
`emitStrPopToLocals(dataLocal, lenLocal)` helpers in
`internal/codegen/wasm/wasm.go`. They emit the canonical
2-slot-push / 2-slot-pop pattern (`local.get data; local.get
len` and `local.set len; local.set data`). No call sites use
them yet; they're scaffolding.

### PW-2 — extend `payloadSlotSize` for strings

Today `payloadSlotSize(StringType, 4) == 4`. Add a
constant + comment marking the post-flip value (`8`). Update
`payloadLayout` to look up via the constant. No behaviour
change.

### PW-3 — pin existing one-slot expectations as tests

Add a unit test asserting current `OpStore`/`OpLoad` of a
string struct field uses 4-byte stride. The test exists to
catch the ABI flip and force the test author to update it
(not to lock the behaviour in).

### PW-4 — wat-side helper signatures, scaffolded

Add `$__lang_str_len_2w(data: i32, len: i32) → i32` and
`$__lang_str_byte_2w(data: i32, len: i32, idx: i32) → i32`
emit-helpers, gated on a feature flag default-off. These are
the post-flip seam helpers; they coexist with the current
single-i32 ones until the flip.

## The atomic flip PR

Touches every `string`-handling site. Outline:

### `internal/codegen/wasm/wasm.go`

- `watType(StringType) → "i32 i32"` (special-cased — wasm
  wants this fanned out to two `i32` results; introduce a
  `watTypes(t)` `[]string` sibling already documented in
  SSO-PLAN.md line 195–201).
- Every `(local $foo i32)` for a string-typed local doubles
  to `(local $foo_data i32) (local $foo_len i32)`.
- `emitStrLenFromLocal($foo)` becomes `local.get $foo_len`
  (no helper call — length is on the stack already).
- `emitStrLenStoreToLocal` removed (no length prefix anymore).
- Every runtime helper:
  - `$__str_eq(a_data, a_len, b_data, b_len) → i32`
  - `$__str_concat(a_data, a_len, b_data, b_len) → (i32 i32)`
  - `$__str_slice(base_data, base_len, low, high) → (i32 i32)`
  - `$__str_idx(base_data, base_len, idx) → byte`
  - `$__lang_str_to_heap(data, len) → (data, len)` —
    spills inline bytes to a fresh heap buffer when needed
  - `$__lang_str_data_ptr(data, len) → ptr` — stays
    similar; spills to scratch for inline
  - `$string_from_bytes(bs) → (i32 i32)`
  - `$__bytes_to_lang_string(host_ptr, host_len) → (i32 i32)`
  - `$args() → string[]` (entry layout changes)
  - `$tcp_recv(conn, max) → (i32 i32)`
  - `$__method_string_as_bytes(data, len) → slice[u8]`
  - `$__stream_read_line() → Option[string]` (entry layout)
- `OpConstStr` emit: two `i32.const`s for both heap and
  inline literals. Heap-form drops the data-segment length
  prefix.

### `internal/codegen/wasm/wasm_ir.go`

- `containsPairMaker` / `containsPairCall` recognise
  string-typed pair returns differently (3-slot or 4-slot
  result for variants carrying string payloads).
- `OpStore` / `OpLoad` for string fields emit two
  store/load pairs.
- `OpStoreLocal` / `OpLoadLocal` for string locals fan out
  to two slots (or a wide-paired-local construct).
- Pair-form rebox needs to know string payloads are 8 bytes
  on wasm now (matches the native ptrW=8 path).

### `internal/ir/ir.go`

- `payloadSlotSize(StringType, 4) → 8`
- `payloadLayout` returns offsets that align to 8-byte
  boundaries for string payloads.
- `WidthPtr` semantics for string types: the IR's "pointer"
  metadata still applies but means "two-slot" on wasm now.
- `pairPayloadWidth` / `payloadWidthForCalleeReturn` map
  string return types to the 2-slot return shape.

### `internal/checker/checker.go`

- `info.FuncSigs` doesn't change (the AST type is still
  `string`); only the IR / codegen layer cares about the
  slot count.

### Tests

Every wasm e2e test that round-trips a string verifies the
new ABI implicitly. Targeted updates for:

- `TestStringsLowerToLinearMemory` — data-segment layout
  loses the length prefix; the literal reduces to N bytes
  (no `\NN\00\00\00` prefix).
- `TestSSOHelpersEmitted` — helper names change to the `_2w`
  suffix pattern; the seams test the new shape.
- All `Test*HelperHasInlineOutputFastPath` tests — the
  inline cap is 7 not 3; encoding is `(data, len)` not a
  single i32; constants change.
- HTTP method preinterns flip from `internOrPackMethod`'s
  single-i32 to a `(data, len)` pair — methodGet now stores
  two values.

## Cleanup PR (after the atomic flip)

- Remove `langstring.TinyInlineCapWasm` / `PackTinyWasm` /
  `IsTinyInlineWasm` / `LengthTinyWasm` / `UnpackTinyWasm`
  (the single-i32 form's compatibility helpers).
- Remove the data-segment length prefix from any remaining
  vestigial uses.
- Unify `$__lang_str_len_2w` → `$__lang_str_len` (drop the
  `_2w` suffix, since it's the only form left).

## Native backend mirror

The arm64 + x86_64 backends already have their own LSB-tagged
SSO scheme (independent of the wasm work; see
`internal/codegen/arm64/arm64.go:840` and
`internal/codegen/x86_64/x86_64.go:2147`). Mirroring the
wasm two-word ABI on natives would touch:

- `internal/codegen/arm64/arm64.go` — every string runtime
  helper + `emitInlineIdxHelper` + the SSO scratch slot at
  `__lang_str_idx_scratch`.
- `internal/codegen/x86_64/x86_64.go` — same shape.

Native already uses 8-byte slots, so the two-word form would
fit naturally. But the LSB-tagged inline encoding would need
to flip to top-bit-tagged to share `langstring.PackInlineNative`
with the IR layer. **Out of scope for the wasm flip arc.**

## Estimated session count

- Pre-work PRs (PW-1 through PW-4): 1 session per PR (4
  sessions total, parallelisable).
- Atomic flip PR: 1–2 sessions (large mechanical change;
  CI-iterate to fix every busted test).
- Cleanup PR: 1 session.

Total: ~6 sessions for the wasm two-word flip. Native mirror
is an additional ~6 sessions per backend.

## Acceptance criteria

- Every existing wasm e2e test passes after the atomic flip.
- New tests added for 4–7 byte inline literals (the new
  cap range): `"GET"` / `"POST"` / `"HEAD"` literals stop
  using the data segment entirely.
- HTTP TODO API + JSON parser + URL encode/decode
  round-trip without regression.
- Module size delta: short-string-heavy programs shrink (no
  data-segment entries for ≤7-byte literals); medium-string
  programs grow slightly (operand-stack-slot count higher
  per string operation).
- No runtime perf regression on the existing benchmarks.
