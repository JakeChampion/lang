# Backend parity tracker

Three code-generation backends ship today:

| backend | OS  | object format | ABI                | status                  |
| ------- | --- | ------------- | ------------------ | ----------------------- |
| arm64   | Linux | ELF         | AAPCS64            | primary target          |
| arm64-darwin | macOS | Mach-O | AAPCS64 + Apple's syscall vector | shares `EmitWithOptions` with arm64 |
| x86-64  | Linux | ELF         | System V AMD64     | newer; some gaps        |
| wasm    | n/a  | wasm32 module | wasm CC + WASI    | the "everything" backend |

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
`__memset`). The lang Map runtime in `prelude.lang` compiles
unchanged. `TestX86_64Map` covers set/get, grow-past-capacity,
string keys, and iter-after-delete.

### File I/O on both native backends — partial

`read_file` / `write_file` work on arm64 + x86-64 today. Both
runtimes wrap `openat(2)` / `read(2)` / `write(2)` / `close(2)`
/ `fstat(2)` and hand-roll the `Result[string, IoError]` and
`Option[IoError]` boxes to match the IR enum layout (16-byte
heap obj with `tag:i32 @0` + pointer payload `@8` on native;
24-byte for `Other(string, string)`; 8-byte for payload-less
variants). Shared `__lang_io_error(errno, path)` runtime does
the errno → variant mapping (ENOENT → NotFound, EACCES →
PermissionDenied, EEXIST → AlreadyExists, EINTR → Interrupted,
default → Other(path, "")).

Coverage: `Test{Arm64,X86_64}ReadFileOk` /
`...ReadFileNotFound` / `...WriteFileOk` /
`...ReadWriteFileRoundtrip` per backend.

Reader / Writer file API ✅ landed: `open_reader` /
`open_writer` / `open_appender` + `Reader.read_line` /
`read_chunk` / `close` + `Writer.write` / `close` on both
natives. Same handle layout (4-byte i32 `fd` at +0; the
allocator rounds up to 16). `stdin()` / `stdout()` /
`stderr()` now return real `Reader` / `Writer` struct
pointers (fd = 0 / 1 / 2) so `stdin().read_line()` flows
through the same `__lang_reader_read_line` runtime as
`open_reader("f").read_line()`. Shared
`__lang_close_fd_box` backs both `Reader.close` and
`Writer.close`. Coverage: `TestArm64ReadLine` plus
`Test{Arm64,X86_64}ReaderWriter` (open + append + read
round-trip, `read_chunk` partial reads, line-by-line
streaming).

### ~~`State[T]` persistent storage~~ ✅ done (program-lifetime interpretation)

Landed on both natives: each `state { var NAME: T = INIT; }` block
becomes a labelled `.data` (literal init) or `.bss` (runtime init)
slot. `OpLoadGlobal` / `OpStoreGlobal` lower to rip-relative
(x86-64) or adrp+ldr/str (arm64) with width chosen by
`stateWidthBytes(t)` (8 for i64 / f64 / pointer-shaped, 4 for
everything else). `OpPersistentSet` / `OpPersistentRestore` are
no-ops (push 0 / drop 16-byte slot) since natives only have one
heap.

For non-literal initialisers the checker synthesises
`__state_init`; arm64 / x86-64 `_start` calls it before `main`,
so all init allocations sit safely below any subsequent
`arena_save`.

Covered shapes (`TestArm64State` / `TestX86_64State`): scalar i32
counter, scalar i64, computed scalar init, `f64 + boolean` mix,
string with concat init, Map[K, V] mutated across function calls.

### ~~Two-cursor allocator for state-rooted mutations~~ ✅ done

Landed alongside the State[T] follow-up. Each native backend
now has two bump-allocator cursors — an arena cursor (mode 0,
the per-request region `arena_save` / `arena_restore`
manipulate) and a persistent cursor (mode 1, never reclaimed).
A 1-byte `__lang_alloc_mode` flag selects which region
`__lang_alloc` bumps; `OpPersistentSet` / `OpPersistentRestore`
real-toggle the flag (no longer no-ops). The IR already wraps
state-rooted method calls in `OpPersistentSet(1)` /
`OpPersistentRestore`, so e.g. `Map.set`'s grow path inside
`handle()` now lands in the persistent region and survives the
request-boundary `arena_restore`.

Coverage: `Test{Arm64,X86_64}StateMapGrowInsideArena` — 50
inserts into a `cap=2` state Map, each wrapped in a
fresh `arena_save / arena_restore` window. Triggers multiple
grows across arena cycles; passes only with the two-cursor
allocator.

Heap layout: two 64 MiB regions (so 128 MiB total virtual
reservation), lazy-mmap'd on first use, hinted at
`0x10000000` and `0x20000000` so both fit in 32 bits (the
prelude's `__store_i32` / `__load_i32` truncation constraint).

---

## Missing IR ops

Ranked by how likely user code is to hit them.

### ~~`OpExtendI32S` / `OpExtendI32U` / `OpWrapI64` on arm64~~ ✅ done

Landed in PR #281. arm64 now has `sxtw x0, w0` for the signed
extension, `mov w0, w0` for both the unsigned extension and the
wrap (32-bit reg form implicitly zero-extends), and a new
`OpConstI64` lowering via `ldr x0, =N` (literal pool). Linux +
Darwin share the encoding so arm64-darwin gets parity for free.

### `OpSignExtend8` / `OpSignExtend16` on both natives

Sub-i32 sign-extension. Used by sub-i32 cast paths
(`var x: i8 = -1 as i8`).

- **arm64:** `sxtb w0, w0` / `sxth w0, w0`
- **x86-64:** `movsx eax, al` / `movsx eax, ax`
- **Risk:** low. Currently masked by the fact that `OpLoadByte` already
  zero-extends, so `as u8 / as u16` paths happen to work.

### `OpLoadI8S` / `OpLoadI16U` / `OpLoadI16S` on both natives

Signed-byte / 16-bit element loads from arrays. Wasm has all three;
native only has `OpLoadByte` (unsigned i8). Affects `i8[]` /
`i16[]` / `u16[]` read paths.

- **arm64:** `ldrsb w0, [x1]` / `ldrh w0, [x1]` / `ldrsh w0, [x1]`
- **x86-64:** `movsx eax, byte ptr [rax]` / `movzx eax, word ptr [rax]` /
  `movsx eax, word ptr [rax]`
- **Risk:** low; one case per op.

### ~~`OpLoadGlobal` / `OpStoreGlobal` on both natives~~ ✅ done

Landed alongside `State[T]`. arm64: `adrp x1, .Lstate_<name>; ldr
w0/x0, [x1, :lo12:.Lstate_<name>]` (width per `stateWidthBytes`).
x86-64: `mov eax/rax, [rip + .Lstate_<name>]`.

### ~~`OpPersistentSet` / `OpPersistentRestore` on both natives~~ ✅ done

Landed as no-ops (push 0 / drop 16-byte slot) — natives only have
one heap, so the wasm-side allocator-mode toggle has no native
analog. See the "State[T]" section for the caveat about
state-rooted Map grow inside `handle()`.

---

## Test coverage parity (no missing codegen, just no tests)

Tests that exist on wasm and would help cover existing native codegen.
Adding them is a copy-paste of the wasm test with a different runner.

### x86-64 lacks (vs arm64)

- ~~`TestX86_64Map`~~ ✅ landed alongside the x86-64 Map runtime
- `TestX86_64IndirectCall` (function-value-in-var — codegen exists,
  no smoke test)
- `TestX86_64Arena` (arena scope reset — codegen exists)
- `TestX86_64EprintExit` (stderr + exit() — codegen exists, no test)
- `TestX86_64DarwinBuilds` — N/A; no Darwin x86-64 target

### Both natives lack (vs wasm)

- `Test*Defer` (defer with conditional / early-return)
- `Test*Switch` (switch-with-break-in-loop)
- `Test*FStringInterpolation`
- `Test*Generic*` (generic function inference, monomorphisation;
  IR-level so likely "just works", but worth a guard test)
- `Test*Tuple*` (destructure / multi-return / heterogeneous)
- `Test*ForEach*` (over-array / break-continue)
- `Test*IfLet*` (if-let match)
- `Test*SubI32*` (u8 / u16 / i8 array writes / slices / widths) —
  blocked on the OpLoadI*/OpSignExtend* gaps above
- ~~`Test*State*` (StateScalarCounter, StateMapAcrossCalls, etc.)~~
  ✅ landed alongside `State[T]` (TestArm64State / TestX86_64State —
  6 sub-cases per backend mirroring the wasm shapes a no-op
  interpretation can express)
- `Test*ReadFile*` / `Test*WriteFile*` / `Test*OpenAppender` —
  blocked on file I/O gap above

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

### arm64-darwin heap-address truncation in Map runtime

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
Darwin) the runtime stores 8 bytes via `__store_ptr`, but the lang
variable's `i32` declaration sheds the high 32 bits. Linux's
`__lang_alloc` hints `0x10000000` so heap pointers happen to fit in
32 bits; macOS ignores the hint and returns high addresses, exposing
the truncation.

**Status**: **confirmed on macOS CI** (PR #291's probe test tripped
the bug — heap-allocated string values stored in a `Map` value slot
get truncated on macOS-latest runners). The probe was removed from
the test suite to keep CI green; re-add it as a regression test
alongside the fix.

**Fix plan (revised after looking at Nature lang's type
system)**: introduce a target-aware **`usize` lang type** modelled
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

Variant: also add `isize` for signed native-width offsets (Rust /
Nature have both). Not required for the bug fix but cheap to
add once `usize` exists.

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

### Wide-scalar Map keys / values (i64 / u64 / f64)

**Scope**: all targets (wasm too — the limit is shared).

**Symptom**: `Map[i64, i32]` or `Map[i32, f64]` doesn't type-check.
The checker constrains K to i32-shaped scalars or string and V to
pointer-sized values.

**Root cause**: the prelude's Map runtime hardcodes an entry stride
of `2 * __ptr_width()` — assumes K and V both fit in pointer-width
slots. Widening requires per-instantiation entry layouts or runtime
type tags.

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
= __load_ptr(...)` locals at the lang level (the value flows as
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

### 1. Register-based `Result[T, IoError]` / `Option[T]` returns

**Impact:** zero-alloc fallible-call returns; saves 16 B + alloc-
cursor bump per `read_file` / `write_file` / `open_*` / Reader/Writer
call. Edge-handler workloads that do file I/O + JSON parse + HTTP
write are dominated by these allocations today.

**Status:** Today every Result / Option return path allocates a
16-byte heap box (`{tag:i32 @0, payload @8}`) via `__lang_alloc`
and returns the pointer. Match dispatches by loading the tag from
the heap.

**Sketch (cribbed from Nature's `errable<T>`):**

- Type system: add a tagged-union representation that lowers to
  a multi-value return. `Result[T, E]` becomes
  `(tag:u8, payload:T_or_E_bytes)` — two registers on native
  (`rax` + `rdx` on System V; `x0` + `x1` on AAPCS64), wasm's
  multi-value `(result)` returns the pair.
- IR: `OpReturnPair` returns two values; `OpMakeOk` / `OpMakeErr`
  / `OpMakeSome` / `OpMakeNone` produce the pair on the stack
  without allocation; `OpMatchTag` peeks the tag without going
  through a load.
- Match codegen reads the tag from the call's register pair
  directly — no heap touch.
- Heap-boxed Option/Result still exists for storing in fields /
  Map values / state slots (where a stable address is needed) —
  emit the box only when the value escapes the call stack.

**Scope:** large. Touches IR, both natives, wasm runtime, every
test that pattern-matches a Result/Option. Plan as a multi-PR
arc: type system → IR → one backend → other backends → prelude
migration.

### 2. Inline small strings (SSO)

**Impact:** zero-alloc for strings ≤ N bytes (typical N = 7 or 15);
significant for short keys, status codes, header names. Today
every literal-empty `""` or runtime-built short string allocates
≥ 16 bytes (alloc round-up).

**Sketch:** repurpose the lang string's runtime representation
from "pointer to (4-byte-length-prefix + data)" to one of:

  - **Niko-style two-word string**: `(data_ptr:usize, len:usize)`
    on the operand stack. Inline-small variant uses high bit of
    `len` to flag "data_ptr field holds inline bytes". 7 inline
    bytes on wasm32 (one pointer-word), 15 on native.
  - **Pascal-style with tagged length**: keep length-prefix
    layout, but for length ≤ 15 use a special heap pool with
    fixed-size cells.

The two-word representation is cleaner and is what most modern
systems languages (Rust, Swift, Zig std) settled on. Breaking:
every string operation in the prelude + native runtimes changes
shape. Pair with the `usize` work — `usize` is the slot type
for the length field.

**Scope:** very large. Pre-requisite: usize (Item 1).

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

### 5. Inline closures with zero captures

**Impact:** the no-capture closure constructor today still
allocates a 16-byte pair `{fn_ptr, env_ptr=0}` (and the env block
allocation is elided by `ElideClosurePair`, but the pair itself
isn't). Common in passing top-level functions as values.

**Sketch:** when `OpMakeClosure` has 0 captures AND the value
flows to an `OpCallClosureDirect` (already statically known via
defunctionalisation), elide the pair entirely — push only the
`env_ptr=0` sentinel for the calling convention's env-slot.

**Scope:** small. Single IR pass extension. Possibly already
half-done by `ElideClosurePair` — needs a closer look.

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

1. **`usize` lang type** (parity Item 1) — building block.
2. **Type-hash Map dispatch** (parity Item 2) — unlocks Items 4
   and 5.
3. **8-byte operand-stack slots** (perf Item 3) — independent;
   parallelisable with 1 & 2.
4. **Register-based Result/Option** (perf Item 1) — large, do
   after 1–3 settle.
5. **Inline small strings** (perf Item 2) — largest, last.
