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

**Caveat for HTTP-handler programs:** the auto-`main` synthesised
for `function handle(req): resp` wraps each request body in
`arena_save` / `arena_restore`. State-rooted mutations that
internally allocate (e.g. `Map.set` triggering a grow) would land
in the arena and be reclaimed at request boundary. The wasm
backend dodges this via the two-cursor allocator that
`OpPersistentSet(1)` toggles into; native still has only one
heap. Filed as a follow-up — the workaround is to size state Maps
generously up-front so grows don't happen inside `handle()`.

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
