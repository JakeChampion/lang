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
- `$__lang_str_len(data, len) → i32` rewritten — heap returns
  `len` as-is; inline extracts bits 24..26.
- `$__lang_str_byte(data, len, i) → i32` rewritten — heap
  loads from `mem[data + i]`; inline splits the index range
  (0..3 → `$data`; 4..6 → `$len`'s low 24 bits).
- `$__lang_str_to_heap(data, len) → (i32, i32)` rewritten —
  inline allocates `byteLen` bytes (no length prefix), copies
  bytes from `$data` + `$len`, returns `(new_ptr, byteLen)`.
  Heap passes through. **Multi-value return — first user of
  the `(result i32 i32)` shape in the SSO seam family; every
  caller will need updating to consume two values.**
- `$__lang_str_data_ptr(data, len) → i32` rewritten — heap
  returns `$data`; inline spills `$data` at mem[0..3] and
  `$len` at mem[4..7], returns `mem[0]`. **Scratch slot grew
  from 4 → 8 bytes — verify no collision with putchar's iovec
  at mem[4..11] before the flip ships.** (It DOES collide;
  needs a different scratch slot or careful sequencing.)
- `emitStrEmpty()` emits two i32.consts now (`data=0`,
  `len=InlineFlagWasm`).
- `OpConstStr` emits two i32.consts now — inline-form via
  `langstring.PackInlineWasm` (≤7 bytes), heap-form as
  `(data_seg_offset, length)` where the data segment **no
  longer has a 4-byte length prefix** (length is on the
  operand stack).

### Outstanding within §1

- `internString` still writes a 4-byte length prefix to the
  data segment for every heap-form literal. The new
  OpConstStr doesn't read it (length comes from
  `i32.const len(op.Str)`). Either drop the prefix from
  `internString`'s data-segment output (saves 4 bytes per
  literal) or accept the waste as dead bytes in a follow-up.
- The `$__lang_str_data_ptr` scratch slot at mem[0..7]
  overlaps putchar's iovec at mem[4..11]. Need to either move
  the scratch slot to a fresh region (e.g. shift everything
  down by 8 bytes) or sequence calls so they can't interleave.

### §4 progress: helpers + caller arity updated

- `emitInlineOutputBuild(outDataLocal, outLenLocal, idxLocal,
  lenLocal, byteAt)` — new signature; emits the inline
  encoding into two locals `$<out>_data` / `$<out>_len` to
  match `langstring.PackInlineWasm` layout. byteAt is invoked
  twice per iteration (once in each branch of `idx < 4`); the
  caller's byteAt closure typically pushes a byte fetch from
  `$__lang_str_byte` or `i32.load8_u` — these closures don't
  yet account for `$__lang_str_byte`'s new 3-arg signature.
- `emitHeapStrAlloc(lenLocal, dstLocal, tailBytes)` — no
  longer offsets the alloc by +4 for the length prefix and no
  longer calls `emitStrLenStoreToLocal`. `$dstLocal` points
  directly at the byte payload.
- All 8 producer call sites of `emitInlineOutputBuild` now
  pass `$<out>_data, $<out>_len` — code compiles but the
  generated WAT references locals that **do not yet exist** in
  the producers' `(local …)` declarations. Modules will fail
  validation at runtime. **Next session:** update each
  producer's locals + function signature + return path
  in lockstep (the byteAt closures need updating too since
  $__lang_str_byte's signature gained a `len` param).

### §4 producers awaiting per-helper migration

Each one needs (1) signature `(param $foo i32)` →
`(param $foo_data i32) (param $foo_len i32)` for each string
input, (2) result `(result i32)` → `(result i32 i32)`,
(3) locals declaration adds `_data` / `_len` pair,
(4) return path pushes both, (5) byteAt closure pushes 3 args
to $__lang_str_byte:

  - $__str_concat       (params: 2 strings; lots of internal
    byte work)
  - $__str_slice        (params: 1 string + 2 ints)
  - $__str_idx          (params: 1 string + 1 int → returns
    byte address; needs careful thought — the address result
    semantics may change)
  - $__str_eq           (params: 2 strings; returns bool)
  - $string_from_bytes  (params: u8[]; returns string)
  - $__bytes_to_lang_string (params: (host_ptr, host_len);
    returns string)
  - $args               (returns string[]; each array slot
    holds a string pointer today — needs slot-layout flip too)
  - $env                (param: 1 string; returns string)
  - $tcp_recv           (param: 2 ints; returns string)
  - $tcp_send           (param: 1 int + 1 string; returns int)
  - $__stream_read_line (returns Option[string])
  - $__method_Reader_read_chunk
  - $__method_Writer_write
  - $__method_string_as_bytes
  - $print / $write / $eprint
  - $open_reader / $open_writer / $open_appender

### §5 progress: emit-side helpers adopt the convention

- `emitPromoteStrParam(local)` now reads `$<local>_data` /
  `$<local>_len` and writes back the two-word
  `$__lang_str_to_heap` result. Callers still pass the bare
  base name (`"$s"`, `"$path"`, `"$name"`) — works as long as
  the producer declares `(local $<base>_data i32) (local
  $<base>_len i32)`. Today's producers don't, so runtime is
  broken at those producers until they're migrated.
- `emitStrLenFromLocal` is doc-noted but body unchanged —
  fastest path is for each caller to switch to passing the
  `$<base>_len` local directly, eliminating the helper call
  entirely; doing it across 20 caller sites is bulk work.

### Layer ordering for the next session

Bottom-up cascade keeps each layer self-tests passing once
the layer is complete:

1. **Helpers, leaf-first** — `$__lang_str_*` SSO seams (DONE),
   `emitStrEmpty` (DONE), `OpConstStr` (DONE),
   `emitInlineOutputBuild` / `emitHeapStrAlloc` /
   `emitPromoteStrParam` (sig flipped — DONE).
2. **Producer runtime helpers** — rewrite each producer's
   function signature, locals, return path, and byteAt
   closure. **THIS IS THE BULK NEXT-SESSION WORK.** Suggested
   order: `$__str_concat` first (most complex byteAt, sets the
   pattern), then mechanical conversion of the others.
3. **wasm IR layer** — OpStore/OpLoad for string fields,
   `watType` fan-out, `watTypes` introduction.
4. **HTTP wrapper** — local fan-out, method preinterns.
5. **e2e iteration** — fix struct field accesses,
   variant-payload reads, pair-form rebox layout shifts.
6. **Native backend mirror** — arm64 + x86_64 in lockstep.

Realistic next-session bite: §2 (producer rewrites) for 4–6
producers, leaving the rest + the IR / HTTP / native work to
following sessions.

## What's broken (deliberately) — 22 e2e tests + 3 unit pins

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
