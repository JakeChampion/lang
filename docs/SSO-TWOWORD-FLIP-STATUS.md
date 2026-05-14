# SSO Two-Word ABI Atomic Flip — In Progress (Draft)

Companion to `docs/SSO-TWOWORD-EXEC.md`. This branch carries
**incomplete** atomic-flip work. **Tests are red.** The doc
exists so the next session opens with a precise read of what's
done, what's broken, and what's left.

## What's done in this branch

- `internal/ir/ir.go:stringSlotSize` flipped from `ptrW` to
  `2 * ptrW`. This is the foundational decision: a string field
  / variant payload / function-arg slot is now 8 bytes on
  wasm32 (16 on natives). Every IR-level offset calculation
  that consults `payloadSlotSize` for `ast.StringType` picks
  this up automatically.

## What's broken (deliberately) — 22 e2e tests, 1 IR test

The single-line flip cascades through:

- **Struct field layouts shift** — every `{… string …}` struct
  now has fields after the string at offset+8 instead of
  offset+4. The wasm `OpStore` / `OpLoad` for the string itself
  still emits ONE i32.store / i32.load; the read on the other
  side gets garbage.
- **Variant payload layouts shift** — `Some(string)` /
  `Ok(string)` / etc. now have payload at offset+8. The
  pair-form rebox already understood ptrW=8 alignment
  (`emitRepackPairAsHeapBox`) but expected 1 store + 1 load.
- **Function param/arg slot counts shift** — a `function
  f(s: string): i32` declared `(param $s i32)` is wrong; needs
  `(param $s_data i32) (param $s_len i32)`. `watType` returns
  `"i32"` for `ast.StringType` and that's threaded through 6
  callers.
- **Runtime helper signatures** — every `$__lang_str_*` /
  `$__str_*` / `$string_from_bytes` / `$args` / `$tcp_recv`
  etc. takes / returns a single i32 string. Today's signatures
  no longer match the operand-stack shape.
- **OpConstStr** emits one `i32.const`. Needs to emit two
  (`data`, `len`).
- **`emitStrEmpty`** pushes one i32. Needs two.

### Confirmed failing tests (from `go test -count=1 ./...`)

```
--- FAIL: TestStringFieldOffsetIsPointerWidth (ir pin test from #373; expected to fail)
--- FAIL: TestWASMReadLineBuiltin
--- FAIL: TestWASMEnvBuiltin
--- FAIL: TestWASMMapStringStringValues
--- FAIL: TestWASMReadFileOk
--- FAIL: TestWASMReadFileNotFound
--- FAIL: TestWASMReadWriteFileRoundtrip
--- FAIL: TestWASMStreamingRoundtrip
--- FAIL: TestWASMReaderReadChunk
--- FAIL: TestWASMOpenAppender
--- FAIL: TestWASMTupleDestructure
--- FAIL: TestWasmPreview2StdinReadLine
--- FAIL: TestWasmPreview2FileRoundtrip
--- FAIL: TestWasmPreview2ReadWriteFile
--- FAIL: TestWasmPreview2HttpHandler
--- FAIL: TestWasmPreview2HttpStateCompiles
--- FAIL: TestWasmPreview2HttpTodoApi
--- FAIL: TestX86_64HttpHandler
--- FAIL: TestX86_64ReadFileOk
--- FAIL: TestX86_64ReadFileNotFound
--- FAIL: TestX86_64ReadWriteFileRoundtrip
--- FAIL: TestX86_64ReaderWriter
```

22 failures total. (Native-side cascades —
`TestX86_64HttpHandler` etc. — confirm the IR layout decision
flows to the native backends; both arm64 and x86_64 will need
parallel updates.)

## What's left, in execution order

### 1. wasm runtime helpers (4 SSO seams)

Rewrite signatures + bodies. Order: simplest → most invasive.

#### `$__lang_str_len`

```
(func $__lang_str_len (param $data i32) (param $len i32) (result i32)
  local.get $len
  i32.const 0x7fffffff
  i32.and   ; mask off the inline flag bit; result is the
            ; byte length whether `len` is the heap-form raw
            ; length or the inline-form length nibble
)
```

NOTE: **`langstring.LengthWasm` has a latent bug** that
surfaces here. It returns `len &^ flag` (bits 0..30), but
`PackInlineWasm` stores length at bits 24..30 and inline bytes
4..6 at bits 0..23. So `LengthWasm(packed)` returns
`(bytes_4_6 | (length << 24))`, not just length. The
WAT-level helper should extract length correctly: for
inline-form it's `(len >> 24) & 0x7f`; the simpler `& 0x7f`
mask at bits 0..6 doesn't work because `PackInlineWasm` doesn't
put length there. Fix `langstring.LengthWasm` to dispatch on
`IsInlineWasm` in a follow-up.

#### `$__lang_str_byte`

```
(func $__lang_str_byte (param $data i32) (param $len i32) (param $i i32) (result i32)
  local.get $len
  i32.const 0x80000000
  i32.and
  if (result i32)
    ; inline: byte position 0..6 spans $data (bytes 0..3) +
    ; $len's low 24 bits (bytes 4..6). Pick the right source.
    local.get $i
    i32.const 4
    i32.lt_u
    if (result i32)
      ; bytes 0..3 live in $data
      local.get $data
      local.get $i
      i32.const 8
      i32.mul
      i32.shr_u
      i32.const 0xff
      i32.and
    else
      ; bytes 4..6 live in $len's low 24 bits, offset (i-4)*8
      local.get $len
      local.get $i
      i32.const 4
      i32.sub
      i32.const 8
      i32.mul
      i32.shr_u
      i32.const 0xff
      i32.and
    end
  else
    ; heap: byte at mem[$data + $i]
    local.get $data
    local.get $i
    i32.add
    i32.load8_u
  end
)
```

#### `$__lang_str_to_heap`

```
(func $__lang_str_to_heap (param $data i32) (param $len i32) (result i32 i32)
  local.get $len
  i32.const 0x80000000
  i32.and
  if (result i32 i32)
    ; inline: alloc $byteLen bytes, copy bytes from $data + $len
    ; encoding into the buffer, return (new_data_ptr, $byteLen)
    ; ... see body sketch in docs/SSO-TWOWORD-EXEC.md ...
  else
    ; heap: pass through
    local.get $data
    local.get $len
  end
)
```

NOTE the new two-result type — this is a multi-value-return
wasm function. Some callers need updating to accept two values.

#### `$__lang_str_data_ptr`

```
(func $__lang_str_data_ptr (param $data i32) (param $len i32) (result i32)
  local.get $len
  i32.const 0x80000000
  i32.and
  if (result i32)
    ; inline: spill $data (4 bytes) + $len's low 3 bytes into
    ; scratch at mem[0..6]; return mem[0]
    i32.const 0
    local.get $data
    i32.store           ; mem[0..3] = $data
    i32.const 4
    local.get $len
    i32.store           ; mem[4..7] = $len's low 4 bytes (only
                        ; 3 are content; mem[7] is byte 6 OR
                        ; length nibble, which the host won't
                        ; read because it's outside `byteLen`)
    i32.const 0
  else
    local.get $data
  end
)
```

### 2. `emitStrEmpty`

Today emits one `i32.const 0x80000000`. Post-flip emits two:

```go
func (g *generator) emitStrEmpty() {
    g.line("i32.const 0")          // data = 0
    g.line("i32.const 0x80000000") // len = flag only, byteLen=0
}
```

### 3. `OpConstStr`

`wasm_ir.go:746` emits one `i32.const`. Needs two:

```go
case ir.OpConstStr:
    data, length := langstring.PackInlineWasm([]byte(op.Str))
    // … check FitsInlineWasm; for heap form alloc a data
    // segment entry and emit (data_ptr, length_const).
    g.linef("i32.const 0x%x", data)
    g.linef("i32.const 0x%x", length)
```

For heap-form (≥8 bytes): drop the length prefix from the data
segment entirely; `internString` returns just the data offset.
Length is on the operand stack as `i32.const <len>`.

### 4. Producer helpers (8 of them — signatures change in lockstep)

Each `$__str_concat` / `$__str_slice` / `$string_from_bytes` /
`$__bytes_to_lang_string` / `$args` / `$tcp_recv` /
`$__stream_read_line` / `$__method_Reader_read_chunk` needs:

- Inputs: take 2-word strings (e.g. `(param $a_data i32) (param $a_len i32)`)
- Output: return 2-word strings (`(result i32 i32)`)
- Internal: the inline-output construction loop already lives in
  `emitInlineOutputBuild` (PR #376); update **just the helper**
  to emit two `local.set` for `(data, len)` instead of one. The
  heap-output path stops calling `emitHeapStrAlloc` with the
  length-prefix store; `emitHeapStrAlloc` (PR #378) updates to
  skip the prefix store.

### 5. Emit-side helpers

- `emitStrLenFromLocal($s)` → `local.get $s_len`. No call
  required; length is on the stack.
- `emitStrLenStoreToLocal` removed entirely (no prefix anymore).
- `emitPromoteStrParam` → takes `(dataLocal, lenLocal)`, calls
  the new `$__lang_str_to_heap` (multi-value return), stores
  both values back.
- `emitStreamsWriteString` → simpler now; `len` is already on
  the stack.

### 6. `watType` / `watTypes`

Introduce `watTypes(t) ([]string, error)` that returns
`["i32", "i32"]` for `ast.StringType`. Migrate the 6 callers
to fan out:

- `internal/codegen/wasm/wasm.go:1275` (params)
- `internal/codegen/wasm/wasm.go:1284` (function-type result)
- `internal/codegen/wasm/wasm_ir.go:350` (function params)
- `internal/codegen/wasm/wasm_ir.go:371` (function result)
- `internal/codegen/wasm/wasm_ir.go:384` (local decl)
- `internal/codegen/wasm/wasm_ir.go:396` (local decl)

For string-typed locals + params, the fan-out uses
`$<name>_data` / `$<name>_len` (or whatever naming convention
the flip picks).

### 7. IR-level OpStore / OpLoad for string fields

`wasm_ir.go`'s OpStore handler emits `i32.store`. For
string-typed fields, must emit **two** stores (offset +0 for
data, +4 for len). Either:

- Introduce `WidthString` width sentinel and dispatch in the
  OpStore handler.
- Or have the IR builder emit two OpStores explicitly when
  storing a string field.

The IR builder side: `payloadStoreOp(t ast.Type) Op` (in
`internal/ir/ir.go`) determines the store op for a payload
type. For StringType today returns `OpStore`. Post-flip needs
to indicate two stores (either via new op or via width
metadata).

### 8. HTTP wrapper local fan-out

`emitHttpHandlerWrapper`'s `$method_str` / `$path_str` /
`$body_str` locals each split into `_data` / `_len` pairs.
`internOrPackMethod` returns a `(data, len)` tuple now —
update each method-enum dispatch arm to emit two `local.set`s.

### 9. Native backends — arm64 + x86_64

The native backends have **their own** SSO scheme (LSB-tagged
inline encoding, separate from wasm's top-bit). The
`stringSlotSize` flip affects native struct layouts too (and
the X86_64 test failures above prove the cascade reaches them).

**Two options**:

- **Option A — native two-word ABI flip in lockstep**: arm64
  + x86_64 backends both flip to the same Niko-style
  `(data, len)` two-register / two-slot string ABI. Drops the
  LSB-tagged inline encoding in favour of the
  `langstring.PackInlineNative` shape (bytes 0..7 in `data`,
  bytes 8..14 in `len`'s low 56 bits, length nibble at 56..59,
  flag at 63). Inline cap goes from native's current (varies)
  to 15 bytes. Substantial per-backend rewrite — likely
  separate session per backend.
- **Option B — native stays one-slot, wasm two-slot**: the
  IR's `stringSlotSize` becomes target-aware, returning
  `2 * ptrW` only on wasm. This avoids the native cascade but
  introduces backend-specific layout decisions — tolerable
  while the native LSB SSO is a pre-existing fork.

The atomic flip's authors should pick a direction. Today's
cascade failures suggest the IR layout decision is shared
between wasm and natives; option B requires more careful
threading of target awareness.

### 10. Cleanup

Once everything passes:

- Remove `langstring.TinyInlineCapWasm` / `PackTinyWasm` /
  `IsTinyInlineWasm` / `LengthTinyWasm` / `UnpackTinyWasm`
  (single-i32 compatibility helpers — superseded).
- Fix the `LengthWasm` bug (see note in §1).
- Update `TestStringFieldOffsetIsPointerWidth` and
  `TestStringParamIsSingleI32Slot` (the #373 pins) to assert
  the post-flip layout.

## Session split estimate

| Session | Scope                                                    |
|---------|----------------------------------------------------------|
| 1       | §1 (4 SSO seam helpers) + §2 (emitStrEmpty)              |
| 2       | §3 (OpConstStr) + §5 (emit-side helpers) + §6 (watType)  |
| 3       | §4 (8 producer helpers — split across sessions if needed)|
| 4       | §7 (IR-level OpStore/OpLoad)                             |
| 5       | §8 (HTTP wrapper) + iterate to passing wasm tests        |
| 6       | §9 (native backends — option A) or target-awareness (option B) |
| 7       | §10 (cleanup) + langstring.LengthWasm fix                |

Realistic: ~7 sessions of focused work for the full flip. The
exec plan in #370 estimated 1–2 sessions for the atomic flip
PR; that was wildly optimistic about scope. The pre-work
landed in PRs #351–#379 is real progress and shrinks the
per-session surface, but doesn't shrink the total work below
this estimate.
