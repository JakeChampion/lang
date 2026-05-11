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

**Fix plan**: widen the prelude's pointer storage to be target-aware.
Options:

1. Introduce a `usize` lang type that's 4 bytes on wasm32 and 8 bytes
   on native. Update `__alloc` / `__load_ptr` / `__store_ptr` to
   return / take `usize`. Update all prelude pointer locals
   (~30 sites in the Map runtime plus the string / slice runtimes)
   from `i32` to `usize`.
2. Widen `__load_ptr` / `__store_ptr` / `__alloc` unconditionally to
   `i64`. On wasm32 the value gets truncated to 32 bits on store,
   which is correct (pointers fit). On native the full 64 bits
   survive. Less type-system change but more semantically awkward.

Multi-day refactor with cross-target testing required. Holding off
until either a real workload hits the bug or option (1)'s `usize` is
useful for other reasons (e.g. native CLI tooling that needs
`isize`-shaped indexing).

### Wide-scalar Map keys / values (i64 / u64 / f64)

**Scope**: all targets (wasm too — the limit is shared).

**Symptom**: `Map[i64, i32]` or `Map[i32, f64]` doesn't type-check.
The checker constrains K to i32-shaped scalars or string and V to
pointer-sized values.

**Root cause**: the prelude's Map runtime hardcodes an entry stride
of `2 * __ptr_width()` — assumes K and V both fit in pointer-width
slots. Widening requires per-instantiation entry layouts or runtime
type tags.

**Fix plan**: monomorphise the Map runtime per K/V instantiation,
with the checker emitting a separate `__map_set_impl_<KH>_<VH>` etc.
per width combination. Or: introduce a runtime entry-shape descriptor
(in the buffer header) that the impl branches on. The latter avoids
binary-size explosion at the cost of branch prediction on the hot
path.

Multi-day refactor. Defer until a real workload needs wide-key Maps;
the current `Map[i32, _]` and `Map[string, _]` cover edge-handler /
HTTP / config / state-cache use cases adequately.
