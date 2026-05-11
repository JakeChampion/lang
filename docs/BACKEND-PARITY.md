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

### File I/O on both native backends

Wasm has `read_file` / `write_file` / `open_reader` / `open_writer` /
`open_appender` / `Reader.read_chunk` / `Reader.read_line` (16+ wasm
tests). **Neither native backend has any of it.**

- **Where to add it:** new runtimes in each `codegen/<arch>/<arch>.go`
  that wrap `open(2)` / `read(2)` / `write(2)` / `close(2)` /
  `fstat(2)` / `lseek(2)`. Most of the heavy lifting is in the wasm
  preview2 layer; on a native Linux target the syscalls map directly.
- **Test:** the `TestWASMReadFileOk` / `TestWASMWriteFileOk` /
  `TestWASMReadFileNotFound` / `TestWASMReadWriteFileRoundtrip` /
  `TestWASMOpenAppender` / `TestWASMReaderReadChunk` /
  `TestWASMStreamingRoundtrip` shapes all transplant directly.
- **Risk:** medium. Each helper is small but there are ~7 of them;
  err-translation (`ENOENT` → `FileError.NotFound`) needs care to
  match the wasm side's tag layout.

### `State[T]` persistent storage

The `state { var counter: i32 = 0 }` syntax that survives across
edge-handler requests on wasi-http. Backed by:

- `OpLoadGlobal` / `OpStoreGlobal` — global-typed slots
- `OpPersistentSet` / `OpPersistentRestore` — allocator-mode toggle
  (route allocations either through the per-request arena or the
  persistent heap)

**Wasm:** all four ops handled.
**arm64 + x86-64:** none handled.

Open question: do these even make sense on a single-process native
binary? The "edge handler" use case implies a multi-request lifecycle
that native binaries don't have. If we want native to compile the same
source as wasi-http, the simplest mapping is "persistent === program
lifetime" — `OpLoadGlobal` / `OpStoreGlobal` lower to ELF `.data` /
`.bss` slots, and `OpPersistentSet` / `OpPersistentRestore` become
no-ops (everything's "persistent" on a native binary).

- **Risk:** low for the no-op interpretation; medium if we want real
  process-survival persistence (would need a manifest + file-backed
  state).

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

### `OpLoadGlobal` / `OpStoreGlobal` on both natives

See "`State[T]` persistent storage" above.

### `OpPersistentSet` / `OpPersistentRestore` on both natives

See "`State[T]` persistent storage" above.

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
- `Test*State*` (StateScalarCounter, StateMapAcrossCalls, etc.) —
  blocked on `State[T]` gap above
- `Test*ReadFile*` / `Test*WriteFile*` / `Test*OpenAppender` —
  blocked on file I/O gap above

### Smaller test gaps (low priority)

- arm64 has an in-line `read_line` case but no `TestArm64ReadLine`
  test function; x86-64 has the full `TestX86_64ReadLine`.

---

## Working order suggestion

The dependency graph is mostly flat. A reasonable greedy order by
risk × leverage:

1. ~~**`OpExtendI32S` / `OpExtendI32U` / `OpWrapI64` on arm64**~~ ✅
   (PR #281)
2. ~~**Map on x86-64**~~ ✅ done — landed in this batch.
3. **`OpSignExtend8` / `OpSignExtend16` + `OpLoadI8S` / `OpLoadI16U` /
   `OpLoadI16S` on both natives** — unblocks sub-i32 tests + array
   handling.  ← *next*
4. **File I/O on both natives** — biggest dollar-amount of code;
   medium risk; high leverage (write a working CLI tool that reads
   stdin / a file is the canonical edge-target story).
5. **`State[T]` on both natives** — depends on whether we want true
   persistence or the program-lifetime-only interpretation. Decision
   first, code second.
6. **Test-coverage parity** — fold these into whichever PR enables the
   underlying feature.

When picking any one of these, the pattern is the same as the closures
PR (#279): mirror the wasm version, add tests at the level the wasm
suite exercises, and re-run the full suite (including wasm e2e) before
opening the PR.
