# Backend parity tracker

Three code-generation backends ship today:

| backend | OS  | object format | ABI                | status                  |
| ------- | --- | ------------- | ------------------ | ----------------------- |
| arm64   | Linux | ELF         | AAPCS64            | primary target          |
| arm64-darwin | macOS | Mach-O | AAPCS64 + Apple's syscall vector | shares `EmitWithOptions` with arm64 |
| x86-64  | Linux | ELF         | System V AMD64     | newer; some gaps        |
| wasm    | n/a  | wasm32 module | wasm CC + WASI    | the "everything" backend |

## CPU baseline

Fern emits **static binaries with no runtime CPU dispatch**, so every
instruction a backend selects is a hard requirement of the produced binary —
an unavailable one is a SIGILL at first execution, not a slow path. The
baselines are therefore a project-level decision, recorded here rather than
inside a codegen switch:

| backend | baseline | what it buys |
| ------- | -------- | ------------ |
| arm64 / arm64-darwin | ARMv8-A, Advanced SIMD included | `clz`, `rbit`, and the SIMD-side popcount (`cnt` + `addv`). Advanced SIMD is mandatory on the ARMv8-A application profile, so this is the architecture floor, not a raise. |
| x86-64 | Haswell-class, 2013 — SSE4.2 + BMI1 (AMD: Piledriver/Jaguar and later) | `popcnt` (SSE4.2) and `lzcnt` / `tzcnt` (BMI1), alongside the SSE4.1 `roundsd` and SSE2 floating point the backend already required. |

**LZCNT / TZCNT have a failure mode POPCNT does not, and it is the reason to
state the baseline rather than assume it.** Below the baseline, POPCNT is an
invalid opcode and faults. LZCNT / TZCNT are the *same opcodes* as the 386-era
BSR / BSF distinguished only by a mandatory `F3` prefix — an older CPU ignores
the prefix and executes BSR / BSF, which answer a different question and are
undefined at a zero input. So a sub-baseline CPU miscomputes **silently** there
instead of crashing.

Anything above these baselines (AVX2, BMI2, …) needs runtime dispatch first;
none of it is used today.

Wasm is the broadest because it was where Map / State / file I/O / preview2
HTTP landed first. The native backends have caught up on the edge-handler
critical path (`function handle(req): resp` → HTTP/1.1 server). Everything
else is on this list.

Each section is a self-contained piece of work — a single PR or two.
We can pick any of them next; nothing here is blocking.

---

## Critical user-visible gaps

These are language features that *compile on wasm but won't compile or
will silently misbehave on a native target.* Highest leverage.

### ~~Maps on x86-64~~ ✅ done

Landed: `map_new` / `__method_Map_*` / `__method_MapIter_*` dispatch
table mirroring arm64, plus the supporting runtimes (`__store_i32` /
`__load_i32` / `__store_ptr` / `__load_ptr` / `__ptr_width` /
`__memset`). The Fern Map runtime in `internal/stdlib/core/map.fern`
compiles unchanged. `TestX86_64Map` covers set/get, grow-past-capacity,
string keys, and iter-after-delete.

### File I/O on both native backends — partial

`read_file` / `write_file` work on arm64 + x86-64 today. Both
runtimes wrap `openat(2)` / `read(2)` / `write(2)` / `close(2)`
/ `fstat(2)` and hand-roll the `Result[string, IoError]` and
`Option[IoError]` boxes to match the IR enum layout (16-byte
heap obj with `tag:i32 @0` + pointer payload `@8` on native;
24-byte for `Other(string, string)`; 8-byte for payload-less
variants). Shared `__fern_io_error(errno, path)` runtime does
the errno → variant mapping (ENOENT → NotFound, EACCES →
PermissionDenied, EEXIST → AlreadyExists, EINTR → Interrupted,
default → Other(path, "")).

Coverage: `Test{Arm64,X86_64}ReadFileOk` /
`...ReadFileNotFound` / `...WriteFileOk` /
`...ReadWriteFileRoundtrip` per backend.

arm64-darwin parity: `read_file` and `now_unix_ms` are ported
to Darwin syscalls — `fstat` uses `fstat64` (BSD 339) with
`st_size` at the 64-bit-inode `struct stat` offset 96 (vs
Linux's 48), and `now_unix_ms` uses `gettimeofday` (BSD 116,
`struct timeval` → `tv_usec/1000`) since Darwin has no
`clock_gettime` syscall. Validated by `TestArm64DarwinNativeReadFile`
and the `now_unix_ms` case in `TestArm64DarwinNativeMachO`, which
execute on the macOS arm64 runner.

Reader / Writer file API ✅ landed: `open_reader` /
`open_writer` / `open_appender` + `Reader.read_line` /
`read_chunk` / `close` + `Writer.write` / `close` on both
natives. Same handle layout (4-byte i32 `fd` at +0; the
allocator rounds up to 16). `stdin()` / `stdout()` /
`stderr()` now return real `Reader` / `Writer` struct
pointers (fd = 0 / 1 / 2) so `stdin().read_line()` flows
through the same `__fern_reader_read_line` runtime as
`open_reader("f").read_line()`. Shared
`__fern_close_fd_box` backs both `Reader.close` and
`Writer.close`. Coverage: `TestArm64ReadLine` plus
`Test{Arm64,X86_64}ReaderWriter` (open + append + read
round-trip, `read_chunk` partial reads, line-by-line
streaming).

### ~~`State[T]` persistent storage + two-cursor allocator~~ ❌ REMOVED

These shipped on both natives and were then **removed** with the
`state` feature (and the arena reset that motivated the second
cursor). For the record, what existed was: `state { var NAME: T
= INIT; }` blocks lowering to labelled `.data`/`.bss` slots via
`OpLoadGlobal`/`OpStoreGlobal`; a synthesised `__state_init`
start function; and a two-cursor bump allocator (an
`__fern_alloc_mode` byte selecting a transient vs. a persistent
region, toggled around state-rooted call sites so a state Map's
grow survived `arena_restore`).

All of it is gone: no `state` syntax, no AST/checker/IR support,
no `OpPersistent*` ops, and the allocator is back to a single
bump cursor (`__fern_heap_ptr`/`_end`) reclaimed by reference
counting. The `Test*State*` cases were removed with the feature.

---

## Missing IR ops

Ranked by how likely user code is to hit them.

### ~~`OpExtendI32S` / `OpExtendI32U` / `OpWrapI64` on arm64~~ ✅ done

Landed in PR #281. arm64 now has `sxtw x0, w0` for the signed
extension, `mov w0, w0` for both the unsigned extension and the
wrap (32-bit reg form implicitly zero-extends), and a new
`OpConstI64` lowering via `ldr x0, =N` (literal pool). Linux +
Darwin share the encoding so arm64-darwin gets parity for free.

### ~~`OpSignExtend8` / `OpSignExtend16` / `OpLoadI8S` / `OpLoadI16U` / `OpLoadI16S` / `OpStoreI16`~~ ❌ REMOVED

Sub-i32 sign-extension and halfword element load/store. Used by
the `i8`/`i16`/`u16` cast and array-element paths. `i8`/`i16`/`u16`
(and the never-used `isize`) were retired in #4408 — the language
now ships `i32/i64/u8/u32/u64/f32/f64/usize` only. `OpStoreI8` and
`OpLoadByte` (zero-extended byte, `u8`) survive; every signed- or
16-bit-width op above no longer exists in `internal/ir`. The
`TestArm64SubI32` / `TestX86_64SubI32` cases these used to cover
(`i32_to_i8_sign_preserved`, `i8_array_signed_sum`, etc.) were
removed or narrowed to `u8` alongside the ops.

### ~~`OpLoadGlobal` / `OpStoreGlobal` / `OpPersistentSet` / `OpPersistentRestore`~~ ❌ REMOVED

These IR ops existed only to support the `state` feature
(global-slot loads/stores and the persistent-allocator-mode
toggle). They were removed with it — the ops no longer exist in
`internal/ir`. See the "`State[T]` persistent storage" section
above.

---

## Test coverage parity (no missing codegen, just no tests)

Tests that exist on wasm and would help cover existing native codegen.
Adding them is a copy-paste of the wasm test with a different runner.

### x86-64 lacks (vs arm64)

- ~~`TestX86_64Map`~~ ✅ landed alongside the x86-64 Map runtime
- ~~`TestX86_64IndirectCall`~~ ✅ landed (function-value-in-var smoke test)
- ~~`TestX86_64Arena`~~ ✅ landed
- ~~`TestX86_64EprintExit`~~ ✅ landed
- `TestX86_64DarwinBuilds` — N/A; no Darwin x86-64 target

### Both natives lack (vs wasm)

- ~~`Test*Defer` (defer with conditional / early-return)~~ ✅ both
- ~~`Test*FStringInterpolation`~~ ✅ both
- ~~`Test*Generic*`~~ ✅ `TestArm64Generic` / `TestX86_64Generic`
- ~~`Test*Tuple*`~~ ✅ both
- ~~`Test*ForEach*`~~ ✅ both
- ~~`Test*IfLet*`~~ ✅ both
- ~~`Test*SubI32*`~~ ❌ REMOVED — `i8`/`i16`/`u16` were retired
  (#4408); `u8[]` sub-i32 coverage lives on in
  `TestArm64SubI32ArithmeticWraps` / the x86-64 equivalent.
- ~~`Test*State*`~~ ✅ landed alongside `State[T]`
- ~~`Test*ReadFile*` / `Test*WriteFile*` / `Test*OpenAppender`~~
  ✅ both natives have ReadFileOk / ReadFileNotFound / WriteFileOk
  / ReadWriteFileRoundtrip + the Reader/Writer suite

### Smaller test gaps (low priority)

- ~~arm64 had no `TestArm64ReadLine` test function~~ ✅ landed
  alongside the Reader / Writer file API PR.

---

## Working order suggestion

The dependency graph is mostly flat. A reasonable greedy order by
risk × leverage:

1. ~~**`OpExtendI32S` / `OpExtendI32U` / `OpWrapI64` on arm64**~~ ✅
   (PR #281)
2. ~~**Map on x86-64**~~ ✅ (PR #282)
3. ~~**`OpSignExtend8` / `OpSignExtend16` + sub-i32 typed loads
   on both natives**~~ ✅ (PR #283)
4. ~~**File I/O on both natives**~~ ✅ — `read_file` + `write_file`
   in PR #284; `open_reader` / `open_writer` / `open_appender` +
   `Reader.*` / `Writer.*` in this batch (Reader / Writer file API).
5. ~~**`State[T]` on both natives**~~ ✅ (PR #286, program-lifetime
   interpretation).
6. ~~**Test-coverage parity**~~ ✅ folded into the parity-test batch
   (PR #287) plus each feature PR.

When picking any one of these, the pattern is the same as the closures
PR (#279): mirror the wasm version, add tests at the level the wasm
suite exercises, and re-run the full suite (including wasm e2e) before
opening the PR.

---

## Supported OS / runtime versions

We support only the **latest** version of each target's host OS or
runtime. The CI runner labels reflect that policy:

- Linux: `ubuntu-latest` (tracks the current Ubuntu LTS image).
- macOS: pinned to a specific recent label (currently
  `macos-15` = Sequoia, on Apple Silicon arm64). We deliberately
  do NOT use `macos-latest` because that floating label has
  shipped beta / regressed images in the past. The pin gets
  bumped in a dedicated PR after verifying the new version
  works (build + test + examples). Pinning to anything OLDER
  than what's currently in `.github/workflows/macos.yml` is
  explicitly not supported.
- wasm: `wasmtime` pinned to a specific version in
  `.github/workflows/ci.yml`. Bumps land as part of the dep refresh
  cycle.

If a future macOS release breaks something:
1. **Preferred**: fix the codegen for the new version.
2. **Acceptable for short-lived breakages**: document the regression
   in this file's "Known limitations" section with a tracking PR
   reference.
3. **Not supported**: pinning CI to an older `macos-N` label to dodge
   the breakage.

---

## Known limitations

Items that are known-broken in some configuration but considered too
costly (or too speculative) to fix right now. Each entry should have a
concrete fix plan and a rough scope estimate.

### arm64-darwin heap-address truncation in Map runtime — RESOLVED

**Resolution**: the `core/map` runtime is now `usize`-pointered
throughout — every Map handle / buffer / entry pointer is a `usize`
local or parameter (`NumberType{Width: WidthPtr}`: 8 bytes native,
4 bytes wasm32), so heap pointers above 4 GiB no longer shed their
high half. No `i32`-typed pointer locals remain anywhere in
`internal/stdlib` (Map / string / slice runtimes). The `usize` type
the fix plan below proposed now exists, and the migration landed with
it. Re-added regression guard: the `map_heap_string_values` case in
`internal/e2e/arm64_darwin_native_test.go` builds a `Map[string,
string]` with concat-built (heap, >4 GiB on macOS) keys + values and
asserts they round-trip on a real Apple Silicon runner (exit 42).

The historical analysis below is retained for context.

**Scope**: macOS-only.

**Symptom**: `Map[K, V]` values that are HEAP-allocated pointers
(e.g. runtime-built strings via `+` concat, structs, arrays) get
truncated when stored in Map value slots. Values that come from
`.rodata` (string literals) work fine because they live below 4 GiB
in the binary's address space; the heap on macOS-14+ is typically
above 4 GiB.

**Root cause**: the prelude declares pointer-holding locals as `i32`:

```
var buf: i32 = __load_ptr(m);   // truncates a 64-bit heap pointer
```

On wasm32 this is correct (pointers are 32-bit). On native (Linux +
Darwin) the runtime stores 8 bytes via `__store_ptr`, but the Fern
variable's `i32` declaration sheds the high 32 bits. Linux's
`__fern_alloc` hints `0x10000000` so heap pointers happen to fit in
32 bits; macOS ignores the hint and returns high addresses, exposing
the truncation.

**Status (historical)**: **confirmed on macOS CI** (PR #291's probe
test tripped the bug — heap-allocated string values stored in a `Map`
value slot get truncated on macOS-latest runners). The probe was
removed at the time to keep CI green; it has since been re-added (see
the Resolution note above) now that the `usize` migration fixes the
underlying truncation.

**Fix plan (revised after looking at Nature lang's type
system)**: introduce a target-aware **`usize` Fern type** modelled
on Nature's `int` (native-width signed) / `uint` (native-width
unsigned). Concretely:

- 4 bytes on wasm32; 8 bytes on native (arm64, x86-64).
- The type checker handles `usize` via a dedicated
  `NumberType{Width: WidthPtr}` marker that backends resolve to
  their target width. We already have `WidthPtr` for `OpLoad` /
  `OpStore` — extending it to type-level uses the same machinery.
- Mixed-width arithmetic policy: `usize + i32` auto-widens the
  `i32` (via the cast inserter from PR #292). `usize + i64` is a
  hard error on wasm32 (i64 doesn't fit in the slot) — explicit
  cast required. On native both are 8 bytes, so the auto-widen
  collapses to identity.
- Helper signatures change from `__alloc(n: i32) → usize` and
  `__load_ptr(addr: usize) → usize` etc. Prelude pointer locals
  become `var X: usize = __alloc(...)`.

Why this beats the spike's "everything is i64" approach:

- **Wasm32 stays 4-byte-pointer-native.** No `i32.wrap_i64`
  shims at every memory op; no IR-level cast hack for `i64 →
  string` on wasm32 (which is the blocker the spike hit).
- **Same code generates the right width on every target** with no
  per-target branches in the prelude — the type system carries the
  intent.
- **Adds a building block that's useful elsewhere** — array
  indexing, slice lengths, file sizes, anywhere "size of a thing
  in memory" appears.

Variant (never implemented, now moot): also add `isize` for
signed native-width offsets (Rust / Nature have both). #4408
retired the idea outright — `isize` had zero uses and `usize`
alone covers every demonstrated need.

### Spike status (post PR #292 attempt)

Tried option (2) end-to-end. Got far enough to confirm scope:

- **Checker changes** are tractable — `addrType = NumberType{Width:
  64}` on the address-taking helpers, plus auto-widening for binops
  / comparisons / function args (the auto-widening half landed
  cleanly in PR #292).
- **Prelude rewrite** is ~140 sites across the Map / string / slice
  runtimes (pointer-typed `var X: i32 = __alloc(...)` → `i64`,
  function signatures, return types). Mechanical but laborious.
- **Native backends** work without code changes — they already
  treat 8-byte slots and 64-bit registers as the default.
- **Wasm32 hits a new blocker**: the IR's `i64 → string` cast is a
  reinterpret no-op everywhere, but on wasm32 the underlying
  memory access (`i32.load` / `call $__str_eq`) needs an i32
  pointer, not i64. Currently the IR doesn't have a target-aware
  "wrap-to-ptr-width" op. Fixing this needs ONE of:
    - A new target-aware IR op (e.g. `OpWrapToPtr`, no-op on
      native / `i32.wrap_i64` on wasm32).
    - Or: widen wasm32's `$__str_eq` and all other helpers that
      take string-pointer args to accept `i64`, internally
      `i32.wrap_i64`-ing.
    - Or: restructure the prelude to type-pun string keys as
      `string` directly rather than i64 (requires per-K
      monomorphisation in the Map runtime — touches Item 2 below
      too).

Estimate: ~2–3 more days of careful work touching IR + wasm
runtime + prelude, with cross-target testing.

### ~~Wide-scalar Map keys / values (i64 / u64 / f64)~~ — RESOLVED

> **Status: RESOLVED.** The runtime-tag scheme was extended instead of the
> type-hash monomorphisation sketched below: `mapKeyKindTag` gained
> **kind 2 = wide-scalar-boxed** (i64 / u64 / f64 keys box into a heap cell
> when `ptrW < 8`, with `__map_hash` / `__map_lookup` dereferencing the
> 8-byte value), `mapValKindTag` was widened to kinds 0..3, and wide-scalar
> V types go through `emitWideMapSet` / `emitWideMapGet`. wasm e2e coverage:
> `TestWASMWideKeyMapBasic` / `…HasDelete` / `…Overwrite` / `…Grow` /
> `…HighBitsDistinct` / `…U64` / `…StringV` / `…KeysSnapshot` plus the
> wide-V `TestWASMMapValuesWideI64` / `…F64` series
> (`internal/e2e/wasm_e2e_test.go`). See the RESOLVED entry in
> `ROADMAP-AND-SELF-HOSTING.md` Part 1 item 3. The full per-(K,V)
> monomorphisation remains a separate, unstarted perf lever
> (`MAP-SPECIALIZATION.md`, #4368). The original analysis below is kept as
> the historical record (note it predates prelude removal).

**Scope**: wasm-only. The natives (x86-64 + arm64 Linux qemu) now
work for `Map[i64, i32]`, `Map[i32, f64]`, `Map[i64, string]`,
`Map[string, i64]`, `Map[u64, i32]`, etc. — each operand-stack slot
is 8 bytes on the natives so the prelude's `(m: i32, k: i32, v: i32)`
signatures coincidentally pass i64 / f64 values through without
truncation, and `__store_ptr` / `__load_ptr` flow the full 8 bytes
through.

**Symptom (wasm only)**: `wasm-tools component new` rejects
`Map[i64, *]` with `type mismatch: expected i32, found i64`. The
typed operand stack on wasm32 enforces strict matching against
`__map_set_impl(i32, i32, i32)` etc., so the IR's `i64` push for
the key fails validation.

**Root cause**: the prelude's Map runtime hardcodes an entry stride
of `2 * __ptr_width()` and assumes K / V both fit in pointer-width
slots. Natives sidestep this via slot-wider-than-declared-type
coincidence; wasm32 doesn't.

**Fix plan (revised after looking at Nature lang's `map<T,U>`
implementation)**: emit a **compile-time type hash** per instantiation
and dispatch a single polymorphic runtime function via that hash.
Nature's `std/builtin/map.n` does this with `@reflect_hash()`; the
runtime takes `anyptr` for the key/value and switches on the
per-instantiation hash constant to pick the right load/store width
+ hash function + comparison.

Translating to our shape:

- Mangle `K` and `V` into stable u32 hashes at the checker layer
  (`mapKeyHash(t) → u32`, `mapValHash(t) → u32`).
- Replace today's `keyKind` / `valKind` runtime fields (i32 enums:
  0 = i32, 1 = string) with the full hash. Same buffer-header
  layout otherwise.
- Per-instantiation `entryStride` computed from
  `widthOf(K) + widthOf(V)` (no longer hardcoded `2 * __ptr_width()`).
- The Map impl's `__load_ptr` / `__load_i32` calls become a small
  switch on the per-entry-half hash — i32 → `__load_i32`, i64 →
  `__load_i64`, string/array/struct → `__load_ptr`, f64 →
  `__load_f64`.

**Shares scope with the arm64-darwin truncation item above.** With
type-hash dispatch, the Map runtime stops carrying `var entryK: i64
= __load_ptr(...)` locals at the Fern level (the value flows as
`anyptr` + per-call width-tagged load). That side-steps the wasm32
"cast i64 → string" blocker we hit in the spike — the Map runtime
no longer needs an `i64 → string` cast at all because string-keyed
maps would dispatch through `__load_ptr` → `string` directly.

Multi-day refactor. Doing this together with Item 1 (usize) is
probably the right shape — both touch the prelude's typed-pointer
locals, and the type-hash dispatch makes the wasm32 blocker
disappear.

---

## Performance / memory wins

Tracked here because the language targets lightweight CLI tools
and edge-handler-style HTTP servers — every byte allocated per
request and every cycle on the hot path matters. Each entry has
a rough impact estimate (mem / speed), a scope estimate, and a
sketch of the design. Mostly **breaking** changes — sequence
them with care.

### ~~1. Register-based `Result[T, IoError]` / `Option[T]` returns~~ ✅ done

**Impact:** zero-alloc fallible-call returns; saves the 8/16 B
heap-box alloc per `Option[T]` / `Result[T, E]` return for
every i32-shaped or pointer-shaped payload type. Edge-handler
workloads that do file I/O + JSON parse + HTTP write no longer
allocate on the happy path.

**Status:** Done. The pair-form ABI lowers a function whose
body returns only variant literals (`Some(x)` / `None` /
`Ok(x)` / `Err(e)` — and tail calls into other pair-form fns,
and ternaries thereof) as a `(tag, payload)` register pair:
- **wasm**: `(result i32 i32)` multi-value return (PR #332,
  #334);
- **x86_64**: `(rax, rdx)` per SysV (PR #336);
- **arm64**: `(x0, x1)` per AAPCS64 (PR #336).
Match-style consumers (`if let` / `match` / `let else`) skip
the heap-box rebox and dispatch on the tag register directly.
User-defined two-variant enums matching the canonical
"payload-carrying variant + nullary variant" shape opt in
automatically (PR #333).

Landed across 17 PRs (#320–#336). Six-lens review of the
midpoint shape captured in `docs/PR-326-REVIEW.md`.

### 2. Inline small strings (SSO) — partially shipped on wasm

**Impact:** zero-alloc for strings ≤ N bytes; significant for
short keys, status codes, header names. Today every runtime-
built short string allocates ≥ 16 bytes (alloc round-up).

**Shipped (wasm, single-i32 form, 3-byte cap)**:
PRs #351–#364 landed the single-i32 tiny SSO encoding (top-bit
flag + 3-bit length + up-to-3 inline bytes, see
`fernstring.PackTinyWasm`) without widening the operand-stack
ABI. Producer flips on `$__str_concat` / `$__str_slice` /
`$string_from_bytes_unchecked` / `$__bytes_to_lang_string` / `$args` /
`$__stream_read_line` / `Reader.read_chunk` / `$tcp_recv` /
`OpConstStr` literals + HTTP wrapper method preinterns. Stream-
write seam (`$__fern_str_data_ptr`) skips the promote-to-heap
alloc on inline-form values written to `$__streams_write`. See
`docs/SSO-PLAN.md` for the full architecture + remaining-work
list.

**Remaining:**

  - **Two-word ABI flip** (lifts cap from 3 → 7 bytes on wasm,
    15 on natives). Repurpose the Fern string's runtime
    representation to `(data_ptr:usize, len:usize)` on the
    operand stack — top bit of `len` flags inline. Niko-style;
    Rust / Swift / Zig std all converged on this shape.
    Pair with the `usize` work — `usize` is the slot type for
    the length field.
  - **Native backend mirror** — x86_64 + arm64 need their own
    `$__fern_str_*` runtime-helper siblings + producer flips.
    Likely 8–10 PRs per backend, paralleling #351–#362.
  - **`Map[string, V]` hash optimisation** — the prelude's
    `__map_hash` does `s[i]` byte-by-byte; under SSO inline
    keys trigger `$__str_idx`-induced promote-to-heap per
    call. Needs either a new `string.byte(i)` primitive or a
    short-key fast path in the hash function.

**Scope:** the wasm single-i32 form is done. The two-word flip
is medium-large per backend; native mirror is large per backend.
Pre-requisite for the two-word form: usize (Item 1).

### 3. Pack the operand-stack into 8-byte slots

**Impact:** halves operand-stack memory; tighter stack frames
mean smaller working-set; fewer cache misses on deep call chains.

**Status:** today every push uses a 16-byte slot regardless of
type, on both natives. The 16-byte slot was a 16-byte-alignment
hedge for `stp` / `ldp` on arm64 and System V's pre-call
alignment.

**Sketch:** drop to 8-byte slots universally. Use unaligned
`str x0, [sp, #-8]!` / `ldr x0, [sp], #8` on arm64 (works on all
ARMv8 targets); use `push rax` / `pop rax` (8-byte native
encoding) on x86-64. Re-align sp to 16 only at call boundaries
(already done via the stack-arg overflow pad).

**Scope:** medium. Touches every push/pop on both natives,
operand-stack offset calculations (closure captures, callee-save
spills, multi-arg call ABI). Per-test re-verification.

### 4. Per-instantiation Map entry sizing (depends on Item 2)

**Impact:** `Map[i32, i32]` entries drop from 16 B → 8 B (50%
memory). `Map[i32, u8]` would drop further (4 B + 1 B = 8 B with
alignment).

Already covered by the wide-K/V item above; reiterated here as
the immediate memory win the type-hash dispatch unlocks. Build
on top of the Item 2 work.

### ~~5. Inline closures with zero captures~~ ✅ done

**Impact landed:** no more 16-byte heap pair per zero-capture
closure-value pass. The classic case — `tryThing(my_lambda)`
where `my_lambda` captures nothing — now materialises as a
static `.rodata` cell (natives) or `closuresBase + 8*ti` static
pointer (wasm), zero alloc.

**What shipped:** new `ir.InlineZeroCaptureClosures` pass runs
after `ElideClosurePair` in every backend's optimisation
pipeline. Rewrites `OpMakeClosure(target, n=0)` →
`OpConstFunc(target)`; both ops produce a pair-pointer of the
same shape (fn_ptr at +0, env_ptr=0 at +ptrW) but OpConstFunc
materialises it via a static cell. ElideClosurePair already
covers the direct-call case (`var f = MakeClosure; ... f(args)`)
by rewriting to OpMakeEnv; this pass closes the orthogonal
escape case where the value flows past direct-call dispatch
(arg to a function-typed param, returned, stored in a field).
Wasm cell init also stopped skipping hoisted entries — every
in-table function now gets a static cell.

### 6. Reduce 4-byte length prefix to varint for short strings

**Impact:** small (saves 0–3 bytes per string), but cumulative
on JSON / HTTP payloads with many short field names. Probably
not worth the complexity.

**Status:** mentioned for completeness; would interact with SSO
(Item 2 in this list).

### Working order

Items 1 + 2 of this list are the highest-impact memory wins; both
depend on the language-level work (`usize`, type-hash Map
dispatch). Item 3 is independent and is a pure memory/speed win
for any program. Item 4 lands as a side effect of the wide-K/V
fix. Items 5 / 6 are smaller and optional.

Suggested sequencing:

1. **`usize` Fern type** (parity Item 1) — building block.
2. **Type-hash Map dispatch** (parity Item 2) — unlocks Items 4
   and 5.
3. **8-byte operand-stack slots** (perf Item 3) — independent;
   parallelisable with 1 & 2.
4. **Register-based Result/Option** (perf Item 1) — large, do
   after 1–3 settle.
5. **Inline small strings** (perf Item 2) — wasm single-i32
   form **shipped** in PRs #351–#364; two-word ABI flip + the
   native backend mirror still ahead.
