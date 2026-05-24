# Small-String-Optimisation (SSO) migration plan

Captures the multi-PR migration strategy for BACKEND-PARITY
perf item #2 (inline small strings). The change is breaking
for **every** string operation in the prelude + native
runtimes + IR codegens, so this doc is the roadmap a reviewer
can use to validate slice boundaries before each PR ships.

## Target end-state representation

`string` on the operand stack becomes **two words**:

```
data_ptr : usize       // points at heap data, OR holds inline bytes
len      : usize       // low bits = length, top bit = inline flag
```

- **Heap form** (`len.top_bit == 0`): `data_ptr` points at the
  string's raw bytes on the heap. Length is `len`. The 4-byte
  length-prefix header used today is gone — `len` lives on the
  operand stack instead of inline with the data.
- **Inline form** (`len.top_bit == 1`): `data_ptr` holds the
  string's bytes directly, packed as `data_ptr_byte_0 ..
  data_ptr_byte_7` (8 bytes), plus `len.byte_8 .. len.byte_15`
  (7 of the bytes from the upper-len word). Inline capacity:
    - wasm32: 7 bytes (one i32 + one i32 = 8 bytes – 1 flag bit)
    - native: 15 bytes (one i64 + one i64 = 16 bytes – 1 flag
      bit)

Stack-slot cost: 16 bytes (was 4-byte ptr + heap header).

## Why two words

- Length lives next to the data pointer, so `len(s)` is a
  no-op (no heap-load) for heap-form strings AND inline.
- Inline-flag bit fits in `usize.top_bit`, leaving 63 bits of
  length on native, 31 on wasm32 — both far more than any
  realistic string.
- Convergent: Rust's `String` (Vec + len), Swift's `String`,
  Zig's std layout, Go's `string` (data, len) all settle on
  this shape. Edge handler / CLI workloads don't push past the
  inline cap often, so the alloc-elision win is dominant.

## Migration steps (one PR per slice)

Each PR ships incrementally — `go test ./...` must stay green
after every merge so the prelude / examples don't break.

### Step 1 — design doc (this PR)

No code change. Validates the plan + reviewer alignment.

### Step 2 — `langstring` Go-side runtime helper

Add a `runtime.LangString` Go struct mirroring the new layout.
Used by code-generators to compute layout constants + check
inline-fit at compile time. No backend wired to it yet.

Files: `internal/runtime/langstring.go` (new), unit tests.

### Step 3 — string-literal lowering on wasm

Change wasm's `OpConstStr` to emit the **two-word** form when
the literal fits in 7 bytes:
- `≤ 7-byte` literal: data_ptr = `(byte_0 << 0) | (byte_1 << 8)
  | ...`, len = `inline_flag | length`. Both pushed as two
  i32s.
- `> 7-byte` literal: data_ptr = heap pointer (current
  behaviour), len = length on stack. Heap-side drops the
  4-byte length prefix.

Every IR / codegen op that consumes a string still expects ONE
operand-stack slot (the old pointer-shaped value). To keep ABI
unchanged at this step, the literal is re-packed onto a
single i32 (heap pointer to a synthetic "two-word view") on
emit; the rest of the pipeline reads it via existing helpers.

Verification: `TestWASMStringLiteralLen`, `TestWASMStringLiteralIndex`,
existing prelude tests.

This step is **carrier-only**: no operations yet use the new
form. Sets up downstream PRs to swap operations in-place.

### Step 4 — `__str_len` runtime helper switch

Replace `__str_len(ptr)` (which reads `*(ptr - 4)`) with
`langstring_len(data_ptr, len)` (returns `len & ~inline_flag`).
Every backend's `len(s)` codegen rewrites to consume the two-
word form.

This is the first step that **drops a heap-load** on the
`len(s)` happy path.

### Step 5 — `__str_concat`, `__str_slice`, `__str_eq`,
`__str_idx`

Each native runtime helper changes signature from
`(i32 ptr, i32 ptr) -> i32 ptr` to `(i64 data1, i64 len1, i64
data2, i64 len2) -> (i64 data, i64 len)`. The function-side
ABI break propagates to every caller in the prelude.

This is the largest step. One sub-PR per helper to keep
review tractable.

### Step 6 — Prelude migration

Walk `internal/prelude/prelude.fern`, update every string-
typed return / arg / local to the new ABI. Most of the source
is unaffected (lang-level code references `string` opaquely);
the prelude's hand-rolled `__str_*` helpers in `wasm.go` /
`x86_64.go` / `arm64.go` are where the work is.

### Step 7 — `OpStore`/`OpLoad` of string fields

`struct Foo { name: string }` field stores / loads now write
two words instead of one. Layout constants in `payloadLayout`,
`structFieldLayout`, etc. change. This is the most fiddly
step — every offset calculation that assumed pointer-shape =
ptrW bytes for `string` needs widening.

### Step 8 — `Map[string, V]` / `Map[K, string]` runtime

Map's hash, eq, and value-store paths all special-case the
string key/value layout. All updated to two-word form.

### Step 9 — Test sweep

Run the full e2e suite under all three backends. Migrate any
remaining test that hard-coded the old layout (e.g., a test
that builds a heap layout by hand and casts).

### Step 10 — Cleanup

Remove the carrier-only repack from Step 3. Remove dead
length-prefix code paths.

## Compatibility / breakage

- Every native runtime helper signature changes.
- The wasm string-literal data-segment layout changes (no more
  4-byte length prefix on the heap side).
- The Go-side AST and IR APIs that compute string layout
  (`payloadSlotSize`, `structFieldLayout`, etc.) change their
  return values for `ast.StringType`.
- `Map[K, V]` runtime helpers (set / get / iter) for string
  keys/values change ABI.

Programs only-using lang-level code remain source-compatible
(no syntax / semantic change to `string` itself).

## Estimated PR count

Steps 1–10 above. Practical sub-splits per native runtime
helper bring this to 12–15 PRs total. Roughly:

- Steps 1, 2, 9, 10: 4 PRs
- Steps 3–4 (wasm carrier + len): 2 PRs
- Step 5 (runtime helpers): 4–6 PRs (one per helper × native)
- Step 6 (prelude): 1 large PR or several thematic ones
- Step 7 (struct field layout): 1 PR
- Step 8 (Map runtime): 1 PR

## Why ship now

Edge handlers spend most of their per-request alloc budget on
short strings (header names like `"Host"`, `"Content-Type"`,
status codes like `"200 OK"`, JSON field names). SSO turns
those allocs into pure stack ops. Memory savings compound
because the freed allocator cursor never bumps, keeping
arena footprint flat across requests.

## What this doc IS

A migration plan, not a design proposal. The Niko-style
two-word representation is the settled-on convergent choice
across Rust / Swift / Zig / Go; we're not redesigning
strings, we're catching up.

## What this doc IS NOT

- A scope estimate for any single PR — each step has its own.
- A claim that all 10 steps will land in this session. The
  pair-form arc (17 PRs) took roughly the same shape; SSO
  will take more.

## Integration touchpoints — wasm-first slice (verified)

Concrete sites the wasm SSO flip needs to touch. Surveyed
during the session that shipped #341 + #342 so the next
session opens with a precise map instead of having to
re-discover the surface.

### Type-system bridge

- `internal/codegen/wasm/wasm.go:6088 watType(t ast.Type)
  (string, error)` — returns `"i32"` for `ast.StringType`
  today. Add a sibling `watTypes(t) ([]string, error)` that
  returns `["i32", "i32"]` for `StringType`. Existing
  `watType` callers either migrate to `watTypes` (fanning
  out over the slice) or stay on `watType` for the single-
  type cases.

### Wasm-side caller sites (each needs the fan-out)

- `internal/codegen/wasm/wasm.go:1257-1266` — function-table
  signatures (`(param p ...)` / `(result r)`). Two i32s per
  string param; multi-value `(result i32 i32)` per string
  return.

- `internal/codegen/wasm/wasm_ir.go:349` — function decl
  param list. Two `(param $name_data i32) (param $name_len
  i32)` per string param. Naming convention TBD.

- `internal/codegen/wasm/wasm_ir.go:370` — function decl
  result. Multi-value if string.

- `internal/codegen/wasm/wasm_ir.go:383` — local decl. Two
  i32 locals per string local.

- `internal/codegen/wasm/wasm_ir.go:395` — scratch slot
  decl. Same as above.

### IR-op handlers

- `OpConstStr` at `wasm_ir.go:747` — `g.linef("i32.const
  %d", g.internString(op.Str))`. For literals ≤7 bytes,
  pack inline via `langstring.PackInlineWasm` → emit two
  `i32.const`s. For >7 bytes, push `(heap_ptr,
  len_with_flag=0)`.

- `OpLoadLocal` / `OpStoreLocal` / `OpTeeLocal` at
  `wasm_ir.go:759-764` — string-typed slots fan out into
  two-word load/store.

- `OpCallDirect` and `OpCallClosureDirect` — args are
  popped off operand stack one slot per arg; string args
  pop 2 slots. Return-handling: string returns push 2
  slots.

- `OpLoad` / `OpStore` for string fields — when reading /
  writing a `struct.string_field`, decide layout (heap +
  inline). Two 4-byte slots in the struct? Or single inline-
  flagged slot? Recommendation: two slots (data, len) for
  uniformity.

- `OpStrLen` / inline `len(s)` (in `ir.go`) — currently
  `[ptr - 4]` load; now `LengthWasm(len)` = mask top bit
  off the `len` word, no heap-load. Pure IR change.

### Wasm runtime helpers (in `wasm.go`)

Each currently takes `i32` (string pointer) per string arg.
Each needs to take `(data, len)` per string arg. Internally
branches on `IsInlineWasm(len)`:

- `$__str_eq` at `wasm.go:2245` — short-circuit on
  (data == data && len == len) for the inline case; byte-
  by-byte for heap case; mixed case needs inline-unpack to
  a temp.

- `$__str_slice` at `wasm.go:2620` — slice. Always returns
  a new string; pack inline if `(high - low) ≤ 7`, else
  alloc + copy.

- `$__str_concat` — concat. Pack inline if total ≤ 7, else
  alloc + copy.

- `$__str_idx` (`emitInlineIdxHelper` family) — single-byte
  index. Inline form reads from `data` / `len`'s low bytes
  via bit-shift; heap reads via `i32.load8_u` at
  `data_ptr + i`.

- `$__lang_str_len` is removed entirely — replaced by the
  inline `len(s)` ⇒ `LengthWasm(len)` IR rewrite above.

### Lang prelude

`internal/prelude/prelude.fern` is ~2k lines. Most prelude
functions have string args / returns and don't change at
the lang-source level — the compiler-side ABI flip means
the lowered code calls the new wasm shapes automatically.
Boundary conversion (e.g., when a string is stored into a
generic `Map[K, V]` whose V is opaque) needs care.

### Native backends

Out of scope for the wasm-first slice. After wasm lands,
analogous work on x86_64 (SysV: 2-register pair-return
already wired via PR #336; same shape for strings) and
arm64 (AAPCS64 `(x0, x1)`). Native inline cap is 15 bytes
(`langstring.InlineCap(8)`).

### Test surface

- `internal/codegen/wasm/wasm_test.go` — string-literal /
  string-len tests assert the old pointer-with-length-prefix
  shape. Each gets a wasm-flip-aware variant.

- `internal/e2e/wasm_e2e_test.go` — most string-using tests
  exercise user-visible behaviour, unchanged. Tests that
  introspect binary layout (rare) need updates.

## Why incremental carriers were rejected

Considered but rejected:

- **Carrier-only steps** that add runtime conversion at
  ABI boundaries without delivering any win until the
  cascade completes. The wasm `OpConstStr → two-word →
  re-pack-to-heap-ptr-for-callers` shape from an earlier
  draft of this doc is anti-pattern.

- **`watType` returns one new + one old representation
  per call**: the IR would have to know which call sites
  accept which, and we'd lose the type system's invariants.

The right shape is **one big atomic flip per backend**, with
the test suite breaking and being fixed over the same PR
chain. No carrier shims.

## Wasm single-i32 form: shipped (PRs #351–#362)

The wasm-first slice landed as the single-i32 tiny SSO form
(3-byte inline cap, top-bit flag + 3-bit length + up-to-3
inline bytes — `langstring.PackTinyWasm`) without widening
the operand-stack ABI. This is a stepping stone toward the
target two-word ABI from the section above; the runtime
machinery + producer flips are in place, so the future flip
lifts the cap from 3 → 7 bytes by widening the slot, not
by rewriting the seams.

### Runtime helpers (in `internal/codegen/wasm/wasm.go`)

- `$__lang_str_len(p)` — inline / heap length read (top-bit
  branch).
- `$__lang_str_byte(p, i)` — inline / heap byte fetch (shift /
  mask vs `i32.load8_u`).
- `$__lang_str_to_heap(p)` — inline → heap promotion (alloc +
  copy + length prefix). For address-arithmetic consumers.
- `$__lang_str_data_ptr(p)` — inline → shared scratch slot
  spill (no alloc). For stream-write consumers that just need
  a synchronously-read pointer.

### Producer flips (each PR delivers one alloc-free path)

| PR    | Producer                          | Inline cap | Notes                                                  |
|-------|-----------------------------------|-----------|--------------------------------------------------------|
| #351  | `$__str_concat`                   | total ≤ 3 | + the SSO seam helpers landed here                     |
| #352  | `$__str_slice`                    | new_len ≤ 3 | mirror of #351                                       |
| #353  | `$string_from_bytes`              | bLen ≤ 3  | cascades into `int_to_string` short outputs            |
| #354  | `$__bytes_to_lang_string`         | host_len ≤ 3 | HTTP method-string marshaling ("GET", "PUT")        |
| #355  | `$args`                           | strlen ≤ 3 | short CLI flags ("-h", "-v"); + `*ast.Index` exprType fix |
| #358  | `$__stream_read_line`             | cur_offset ≤ 3 | interactive prompts ("y\n", "ok\n")               |
| #359  | `$__method_Reader_read_chunk`     | n ≤ 3     | one-byte status probes, two-byte framing tokens        |
| #359  | `$tcp_recv`                       | n ≤ 3     | short protocol tokens                                  |
| #361  | `OpConstStr` literal pack         | len ≤ 3   | module-size win: short literals stop using data segment |
| #362  | HTTP wrapper preinterned methods  | len ≤ 3   | pointer-eq fast path for `req.method == "GET"`         |

### Consumer flips

- `OpStrLen` IR-handler + `emitStrLenFromLocal` seam → `$__lang_str_len`.
- `$__str_eq` byte fetches → `$__lang_str_byte` (handles inline-vs-heap).
- `$__str_concat` byte fetches → `$__lang_str_byte`.
- Stream-write paths (`$print` / `$write` / `$eprint` /
  `$__method_Writer_write` / `$tcp_send` / HTTP body write
  / `emitFdWriteString`) route through `$__lang_str_data_ptr`
  — PR #357. Zero per-call alloc on inline-form strings.
- Address-arithmetic paths (`$__str_idx`, `$__str_slice`,
  `emitOpenHelper`, `emitOpenViaStreamHelper`, `$env`,
  `$__method_string_as_bytes`) promote-to-heap at entry via
  `$__lang_str_to_heap`.

### Latent-bug fixes surfaced by the flips

The producer flips uncovered a family of pre-existing latent
bugs where `exprType` in `internal/ir/ir.go` had no dispatch
for several expression types — the missing-case fallback was
the array-shape `[ptr - 4]; load`, which traps on inline-form
strings.

| PR    | Case added                | Manifestation                                  |
|-------|---------------------------|------------------------------------------------|
| #355  | `*ast.Index`              | `len(string_array[i])` crashed on inline strings |
| #356  | `*ast.Call`               | `len(int_to_string(n))` crashed for 1..3-digit n |
| #360  | `*ast.IfExpr` + `*ast.MatchExpr` | `len(if … else …)` / `len(match …)` crashed when an arm returns inline |

`*ast.IfLet` doesn't need a case (its Then/Else are `Stmt`,
not `Expr`). One known TryOp-on-`Option[string]` bug remains
unresolved (filed for follow-up) but reproduces on heap-form
strings too — not SSO-induced.

## Remaining work

In rough order of payoff:

1. **Two-word ABI flip** (lifts cap from 3 → 7 bytes). Big
   per-backend change; the runtime helpers already abstract
   the inline/heap branch so most consumer-side code stays
   untouched.
2. **Native backend SSO** (x86_64 + arm64). Each backend needs
   its own `$__lang_str_*` siblings in raw assembly. PR
   sequence likely mirrors the wasm shape (#351–#362) per
   backend.
3. **`Map[string, V]` runtime in the prelude**. The hash
   function does `s[i]` byte-by-byte; under SSO, inline keys
   trigger `$__str_idx`-induced promote-to-heap per call.
   Needs either a new `string.byte(i)` primitive or a
   prelude-only fast path for short keys.
4. **TryOp on Option[string]** — existing bug where the
   pair-form rebox stores payload at the 8-byte-offset
   layout (arm64 shape) but the success-path read targets
   the 4-byte offset (wasm shape). Not SSO-induced;
   reproduces with heap strings.


https://claude.ai/code/session_01LXybxbbVBbwLFHmbYAobhN
