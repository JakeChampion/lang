# Roadmap: tech debt + self-hosting readiness

Captures the state of the codebase as of 2026-05-15. Combines
two audits — current tech debt (what's wrong / behind / risky)
and self-hosting readiness (what's missing to rewrite the
compiler in lang itself). Companion to `BACKEND-PARITY.md`,
`SSO-PLAN.md`, `MAP-SPECIALIZATION.md`.

## Part 1 — Tech debt

Ranked roughly by severity. Each item includes file refs, why
it's debt, and the shape of a fix. The first three are on the
critical path for production parity; items 4 and 5 unblock
follow-on perf work.

### 1. ~~arm64-darwin Map heap-address truncation~~ — RESOLVED

Originally the audit's top critical item. `usize` landed as
a target-aware NumberType width earlier; this PR migrates the
Map runtime's pointer-typed params + locals
(`m`, `k`, `v`, `buf`, `entriesBase`, `entryK`, etc.) from
`i32` to `usize`, preserving the full 8-byte address through
every helper on arm64-darwin's high heap. `__map_hash` keeps
its `k: i32` param because wide-scalar K boxing already
ensures the value fits in 32 bits via the cell-pointer route
— a future PR can flip it once a dedicated wide-scalar K
hash entrypoint exists. The `map_heap_value_probe` test's
Darwin skip has been removed; macOS CI now exercises it
alongside Linux.

### 2. ~~Closures-with-captures only lower on wasm~~ — RESOLVED

Originally tracked as a feature gap. On a follow-up audit
the implementation was already in place on every backend:

- `internal/codegen/arm64/arm64.go:emitMakeClosureOrEnv`
- `internal/codegen/x86_64/x86_64.go:emitMakeClosureOrEnv`
- `internal/codegen/wasm/wasm_ir.go:emitMakeClosureFromIR`

Test coverage: 8 `TestArm64Closure*` cases plus matching
counts on x86-64 + wasm, all green. The original audit was
based on a stale `CLAUDE.md` line that has since been
updated. Item is closed — leaving the entry in the doc so
the audit history is preserved.

### 3. ~~Wide-scalar Map K/V (i64 / u64 / f64) silently
   diverges across backends~~ — RESOLVED

- **Where**: `internal/ir/ir.go` `mapKeyKindTag` /
  `mapValKindTag`. The doc previously claimed both only
  knew "0=i32-scalar / 1=string / pointer" — that's stale.
- **Resolution**: the runtime-tag scheme was extended.
  `mapKeyKindTag` now includes **kind 2 = wide-scalar-boxed**
  for i64 / u64 / f64 keys when `ptrW < 8` (wasm32); the
  IR boxes the key into a heap cell and `__map_hash` /
  `__map_lookup` dereference to hash/compare the underlying
  8-byte value. `mapValKindTag` was widened (kinds 0..3 for
  i32-scalar / non-array-pointer / non-rc array / rc-tracked
  array), and wide-scalar V types use the `emitWideMapSet` /
  `emitWideMapGet` boxing codepath.
- **Coverage** (wasm e2e): `TestWASMWideKeyMapBasic`,
  `…HasDelete`, `…Overwrite`, `…Grow`, `…HighBitsDistinct`,
  `…U64`, `…StringV`, `…KeysSnapshot`, plus wide-V
  `TestWASMMapValuesWideI64` / `…F64` and the boxing-dance
  series at `wasm_e2e_test.go:2446`+.

### 4. Map runtime-tag dispatch in hot paths — perf + code size

- **Where**: `internal/prelude/prelude.fern` —
  `__map_hash`, `__map_lookup`, `__map_set_impl`,
  `__map_delete_impl`, `__map_keys_impl`,
  `__map_values_impl`. `docs/MAP-SPECIALIZATION.md` plan
  exists; 0 of 5 steps started.
- **Why**: every probe step pays a runtime `if (keyKind ==
  0)` branch, the hash function takes a tag arg, the
  keys()/values() snapshot pays a `if (valKind == 1)` check.
  Every Map-using binary carries both the scalar and string
  hash bodies even when only one is used (the tree-shaker
  can't eliminate dead branches inside a function).
- **Fix**: 5-step monomorphization via existing
  `internal/monomorph` (the machinery, mangling, and
  re-check pass already work for user-defined generic
  functions). Plan documented in `MAP-SPECIALIZATION.md`.

### 5. 16-byte operand-stack slots on natives — memory + perf

- **Where**: `internal/codegen/arm64/arm64.go:4434`
  (`slotBytes = 16`), `internal/codegen/x86_64/x86_64.go:1678`
  (same).
- **Why**: every push uses 16 bytes regardless of type — a
  16-byte alignment hedge for arm64 `stp` / `ldp` and System
  V pre-call alignment on x86-64. Halving slots to 8 bytes
  is *not* a simple constant flip: an experiment patching
  both `slotBytes` values to 8 (no other changes) **builds
  cleanly but SIGSEGVs 55 of the e2e fixtures**, because
  the existing codegen relies on every push being an even
  multiple of the call-boundary alignment requirement
  (16 bytes for arm64 AAPCS64 and x86-64 SysV) — every
  push currently maintains the invariant "for free".
  Switching to 8-byte pushes makes parity toggle each push,
  so calls land at an unaligned `sp` and the callee's stp /
  push of a save register faults. The alignment requirement
  matters at call boundaries; what's missing is the
  bookkeeping to re-align there.
- **Fix**: switch arm64 to unaligned `str x0, [sp, #-8]!`,
  x86-64 to `push rax`. Re-align sp to 16 only at the
  `bl` / `call`. Touches every push/pop, callee-save spill,
  multi-arg call layout. Medium scope, independent of items
  1-4.

### Latent bugs (not user-visible yet)

- ~~**`internal/codegen/arm64/arm64.go:4088`** — Linux-only
  syscalls crash at *codegen time* on arm64-darwin~~ — fixed
  in PR #391. The pre-scan now detects `read_file` calls on
  Darwin and surfaces a clean error from `EmitWithOptions`
  before any asm is written. The codegen panic stays as a
  defence-in-depth assertion for future helpers that grow
  Linux-only syscalls without a corresponding pre-scan entry.
- **`internal/ir/ir.go:2531,2660`** — i64 → string cast
  returns `"not yet supported"`. Dead today (checker rejects
  it pre-IR), but a latent blocker for the wide-scalar +
  usize work.
- **`internal/ir/ir.go:4717`** — `assignment target %T not
  yet lowered`. Dead code path (checker would catch it
  first); signals an unfinished lowering case.

### Sprawl signals

- **Runtime-tag injection in 3 places** (`ir.go`,
  `checker.go`, `ast.go`) — item 4 will consolidate.
- **`ptrW == 4` checks scattered across ~12 sites** —
  partially unified via `ast.UseTwoWordStrings`; SSO work
  is finishing the consolidation.
- **SSO native-mirror flip in flight** — wasm32 shipped in
  PR #382, arm64 in PR #383 + follow-ups. x86-64 mirror is
  the next chunk; estimated 10-15 PRs.

---

## Part 2 — Self-hosting readiness

The compiler is written in Go (`cmd/fern/main.go` +
`internal/{lexer,parser,ast,checker,monomorph,closureconv,ir,codegen,interp,treeshake,diag,modload,prelude}`).
Self-hosting means rewriting it in lang itself and
bootstrapping from a previous version. Rough estimate:
**~60% portable today** at the time of this audit. The hard
blocker (union types) has since landed — see the resolved
section below. Updated estimate: ~75% portable.

### ✅ UPDATE: a convergent self-hosting fixpoint is achieved

The `examples/self_host/` Fern port — lexer + recursive-descent
parser + x86-64 emitter (`asm.fern`) + module-flattening
(`flatten.fern`) + a stdin driver — now **compiles its own source
to a byte-identical compiler**. The bootstrap chain
(`internal/e2e/self_host_fixpoint_test.go`):

```
stage 0  Go compiler builds bundle_run (the multi-module driver)
stage 1  bundle_run bundles lexer+parser+asm+flatten+io+driver -> mmc
stage 2  mmc compiles its own source                           -> gen2
stage 3  gen2 compiles its own source                          -> gen3
```

`mmc`, `gen2`, and `gen3` are all **byte-identical** (~3.67 MB of
asm) — the Go-bootstrapped and self-hosted compilers produce exactly
the same output, and the self-hosted compiler reproduces itself.
`gen2` also compiles independent programs (e.g. a 2-module
`a.add(19,23)`) to working binaries. The earlier one-trailing-newline
gap between `mmc` and `gen2` (Go `print` = puts vs the self-host
emitter) was closed by adding a no-newline `write` builtin and
switching the driver to it, so the convergence is now total from
stage 1.

The same fixpoint holds on **ARM64** (`TestSelfHostFixpointArm64`,
CI-gated under qemu-aarch64): the Fern-authored `asm_arm64.fern`
emitter reproduces itself byte-for-byte too.

The driver no longer relies on any compiler-injected I/O shortcut:
the bundle carries the **unmodified** `internal/stdlib/std/io.fern`,
and the self-hosted compiler reads its own stdin through the real
`std/io.read_all_stdin` (`stdin` + `Reader.read_chunk` + `Some`/`None`
+ `match`). Both emitters lower that Reader / Option machinery
(`__fern_reader_read_chunk` / `__fern_reader_close`, Option boxes
`[tag@0, payload@8]`, tag-discriminated `match` with payload binding).

The walls cleared to get here (all in `examples/self_host/`, gated by
`internal/e2e/self_host_*_test.go`): O(N²) output build → `strbuf`;
parser non-advance runaways on qualified names
(`parse_type_name` / `parse_pattern`) and qualified struct literals;
`strbuf` + `read_all_stdin` builtins + a 256 MiB heap in the emitter;
the no-newline `write` builtin (stage-1 byte-identity);
struct-field-read and method-call-result type inference; module
flattening (qualified-ref rewrite + own-decl mangling + bundle);
amortised-O(1) array `push` (geometric growth with a hidden capacity
word); the ARM64 emitter brought to self-hosting parity; and the
Reader / Option lowering on both backends (so the fixpoint compiles
the real `std/io`). The original "minimal fix … ~1-2 weeks" /
"6-9 weeks" estimates below are superseded by this result.

### ✅ UPDATE: real CLI toolchain + import-driven file loading

The self-host emitters have since grown the surface a real compiler
driver needs, all gated by `internal/e2e/self_host_*_test.go` and
cross-checked against the Go backend (both x86-64 and arm64):

- **argv + file I/O**: `args(): string[]`, `arg_at` / `args_count`,
  `read_file(path): Result[string, IoError]` (`openat` + `lseek`-sized
  read + `Ok`/`Err` match), `write` (no-newline output).
- **integer ops**: `expr as Type` casts (mask / sign-extend per width),
  bitwise + shift operators `& | ^ << >>` (added to the parser
  precedence table — they were silently dropped before).
- **byte arrays**: `__alloc_u8(n)`, `string_from_bytes(arr)`, and array
  element assignment `arr[idx] = val`. Together these compile the real
  `std/hex` and `std/base64` (`TestSelfHostBytesX86_64`).

On top of those, `examples/self_host/asm_load_run.fern` is an
**import-driven, file-loading driver**: given an entry `.fern` path it
follows `import "./x"` declarations to sibling files on disk
(`read_file` + the parser's `Import` list), loads them transitively,
and compiles the merged set — no stdin marker-bundle. It reaches its
own **file-driven self-hosting fixpoint** (`TestSelfHostLoadFixpointX86_64`):
the Go-built loader compiles its own source resolving
lexer + parser + flatten + asm from disk → gen1, and gen1 → gen2
byte-identical. `examples/self_host/asm_file_run.fern` is the simpler
single-file CLI shape (compile one file by path).

### ✅ UPDATE (2026-06): unified `fern` CLI + a self-hosted wasm backend

Two things landed on top of the native (x86-64 / arm64-linux) self-host
since the notes above:

**Unified `fern` CLI** (`examples/self_host/fern.fern`). Where the
codebase previously had a dozen single-mode `*_run.fern` shims, there is
now one self-hosted binary that parses argv flags and dispatches —
`fern [-check | -interp | -fmt] [-target x86-64|arm64|arm64-darwin|wasm]
[-o OUT] <entry.fern> [stdlib-root]` — reusing the import-driven file
loader.
It hosts both native emitters (`asm.fern` + `asm_arm64.fern`) plus the
checker, interpreter, printer, and the wasm emitter, selected at runtime.
Gated by `internal/e2e/self_host_cli_test.go`.

**A third self-host backend: wasm** (`examples/self_host/wasm.fern`,
driven by `wasm_run.fern` and `fern -target wasm`). It emits a WASI core
module in the text format (WAT) — `_start` calls `proc_exit(main())`, so
`wasmtime run prog.wat` exits with the program's result. Built up across
~20 incremental, differential-tested slices (each its own PR, gated by
`internal/e2e/self_host_wasm_emit_test.go`, run end-to-end under
`wasmtime`), it now compiles the **full non-generic-monomorphised core
language**:

- integers with **non-trapping** div/rem (matching the native backends:
  `x/0=0`, `x%0=x`, `INT_MIN/-1=INT_MIN`), comparisons, logical ops;
- locals, `if`/`else`, `while`, `break`/`continue`, early `return`;
- free functions, recursion, mutual recursion, and **receiver methods**
  (`$RecvType__name`, receiver passed first; return types flow through
  the type tracker);
- the full **string** library (heap `[len][bytes]` blocks: concat,
  `==`/ordering, `len`, `starts_with`/`ends_with`/`contains`/`index_of`,
  `to_upper`/`to_lower`/`repeat`, `join`/`split`), `print`/`write`/
  `print_int` via `fd_write`, a bump allocator;
- **arrays** (`[len][cap][elems]`): literals, index, `.len()`, `.push()`
  with geometric growth, index-assignment, `for…in`; i32 **and** string
  element typing;
- **structs** (`[type_id][fields]`): literals, field read/assign, `{
  ...base }` update, nesting, struct-typed params/returns;
- **Option/Result** tag boxes + `match` + the `?` operator, with typed
  Some/Ok payloads (string/array/struct);
- **struct-union `match`** (`type E = A | B`) dispatching on the type id;
- **generics by erasure** (the parser drops the `[T]` lists; the backend
  compiles one body per decl);
- the **wasi runtime builtins**: `env` (environ_get), `random_bytes`
  (random_get), `args` (args_get), `read_file` / `write_file`
  (path_open + fd_read/fd_write), and the **clocks** `now_unix_ms` /
  `monotonic_ns` / `now_ns` (clock_time_get);
- a real **i64 value path** — i64-typed locals / params / returns, i64
  arithmetic (`+ - * /` `%` via guarded 64-bit div/rem) and comparison,
  literal coercion into i64 sinks, and an i64 decimal formatter — so the
  64-bit clock timestamps (which exceed the i32 range) round-trip and
  print correctly;
- an **f64 floating-point path** mirroring the i64 one — f64 locals /
  params / returns, arithmetic + comparison, the `as` casts (f64↔i32/i64,
  with `i32.trunc_f64_s` / `f64.convert_*` and integer narrowing masks),
  and the primitive math builtins (`__sqrt_f64` / `__floor_f64` /
  `__ceil_f64` / `__trunc_f64` / `__round_f64` / `__abs_f64`);
- **maps** — a `Map { … }` literal (desugared to `map_new[_i32](n).set(…)…`)
  with `.set` / `.get` (→ `Option`) / `.get_or` / `.has` / `.len`, backed by
  an open-addressing hash runtime (linear probing, load-factor-0.75 growth
  + rehash). Both **i32 keys** and **string keys** (content hash via FNV-1a
  + `__fern_streq` compare, selected by a key-kind flag in the map box), and
  both **i32 values** and **string values** (value-type tracking types
  `.get` / `.get_or` results so they print / concat correctly), plus
  `.delete` (tombstone deletion — the probe skips tombstones, `set`
  reclaims them, and growth/rehash drops them), `.keys` / `.values`
  (snapshot arrays in probe order — `string[]` for string keys / values, so
  `for k in m.keys()` / `for v in m.values()` type correctly), and
  `for (k, v) in m` direct pair iteration (walks the live slots, binding
  k / v with the right key / value type, honouring break / continue). The
  **map surface is now complete**.

Gated by 334 differential cases as of this writing. What remains for the
wasm backend to retire the Go wasm path: the **`wasi:cli/run` /
`wasi:http` component shapes** (the Component-Model packaging in
`internal/wasm/component`, ported to Fern, on top of this core module),
and binary wasm encoding (today it emits WAT text, runnable directly by
`wasmtime`).
The core wasi builtins (clock / file / env / random) are now covered.

### ✅ UPDATE (2026-06): self-hosted arm64-darwin (Mach-O) target

`fern -target arm64-darwin` now emits arm64-apple-darwin assembly that
clang + ld64 / lld link into a Mach-O executable on Apple Silicon —
closing the one native backend the self-host had that the Go compiler
already supported. It reuses `asm_arm64.fern`'s instruction selection
verbatim; a post-pass, `asm_arm64.darwinize`, reskins the GAS output
into the Mach-O dialect:

- PC-relative addressing `adrp X, sym` / `add X, X, :lo12:sym` →
  `adrp X, sym@PAGE` / `add X, X, sym@PAGEOFF` (the identical ADRP/ADD
  relocation pair, Apple syntax);
- sections `.section .rodata` → `__TEXT,__const`, `.bss` →
  `__DATA,__bss`; ELF-only `.type` / `.size` / `.note.GNU-stack` dropped;
- entry `_start` → `_main` (ld64 / lld's default, invoked via LC_MAIN);
- the number-compatible syscalls (read / write / close / openat / lseek /
  exit / mmap) remapped to the Darwin BSD vector with `svc #0x80`.

Mirrors the Go backend's `EmitWithOptions{Darwin}` branch one-for-one.
Supported surface is the **core language** (arithmetic, control flow,
functions, structs, enums, arrays, strings incl. heap concat, closures,
maps, stdout/stdin I/O).

Two kinds of Darwin divergence, handled in two places:

1. **Error convention** (all fallible syscalls): Darwin returns `+errno`
   with the carry flag set; Linux returns `-errno`. `darwinize` injects
   `b.cc <lbl>; neg x0, x0; <lbl>:` after each remapped `svc #0x80` so the
   self-host's `x0 < 0` checks see Linux-shaped `-errno`. (`exit` skipped.)
2. **Structural / constant differences** (struct layout, chunking, flag
   constants) — emitted directly by the code generator via a `darwin`
   flag threaded into `asm_arm64.emit_module`, since the lexical post-pass
   can't restructure them.

Ported so far via the flag:

- `now_unix_ms` → `gettimeofday` (BSD 116; Darwin has no `clock_gettime`
  syscall), reading `struct timeval`'s `tv_usec` and dividing by 1000;
- `random_bytes` → chunked `getentropy` (BSD 500, 256-byte cap per call,
  no flags arg) in place of Linux `getrandom`;
- `read_file` + `write_file` — number-compatible syscalls
  (openat / lseek / read / write / close; read_file sizes via `lseek`,
  not `fstat`, so no struct-stat dependency) plus the right open-flag /
  `AT_FDCWD` constants (`O_WRONLY|O_CREAT|O_TRUNC` is 1537 on Darwin vs
  577 on Linux; `AT_FDCWD` is -2 vs -100) and errno normalisation, so
  both succeed *and* report errors correctly.
- `stat` / `FileStat` — `newfstatat` (#79) -> Darwin `fstatat` (#470,
  same args) + the `struct stat` field offsets (`st_mode` is a u16 at
  offset 4 / `st_size` an i64 at offset 96 on Darwin's 64-bit-inode
  stat, vs u32@16 / i64@48 on Linux arm64). `S_IFMT`/`S_IFREG`/`S_IFDIR`
  are the same POSIX constants. is_file / is_dir / size all read back
  correctly.
- `remove_file` — `unlinkat` (#35) -> Darwin #472 (same args, flags 0)
  + `AT_FDCWD` -2 + errno normalisation.
- `monotonic_ns` — no syscall: reads the architectural counter
  `CNTVCT_EL0` scaled by `CNTFRQ_EL0` to ns (this is what
  `mach_absolute_time` does on Apple Silicon). EL0-readable; the Go
  backend never ported this, so the self-host is ahead here.
- `temp_dir` — `mkdirat` (#34 -> Darwin #475) + `AT_FDCWD` -2; its unique
  suffix comes from the now-ported `monotonic_ns`.

Still on their Linux encoding (out of scope, documented gaps): the
directory-enumeration builtins (`read_dir` / `remove_dir_all` use
`getdents64`, whose Darwin equivalent `getdirentries` has a different
`dirent` layout — the genuinely structural one) and the subprocess
family. These are emitted
but only misbehave if a program actually calls them on Darwin.

Gated by `internal/e2e/self_host_macho_test.go` — cross-links each
program with clang + lld and asserts a valid arm64 Mach-O executable off
Apple Silicon, and on the macOS arm64 CI runner builds the self-host CLI
natively, runs it, and **executes** the emitted Mach-O (incl. the
`now_unix_ms` / `random_bytes` / `read_file` / `write_file` cases)
asserting exit codes — plus the `darwinize` + Darwin-builtin self-test
cases in `asm_arm64.fern`'s own `main()`.

### ~~Hard blocker — no interface / union-of-struct polymorphism~~ — RESOLVED

Originally the audit's main blocker. Landed in PR #390
(core) + #392 (implicit struct → union wrap):

```
struct Add { l: i32, r: i32 }
struct Lit { v: i32 }
type Expr = Add | Lit;

function eval(e: Expr): i32 {
    match (e) {
        Add(a) => { return a.l + a.r; },
        Lit(l) => { return l.v; },
    }
}

function main(): i32 {
    var e: Expr = Add { l: 1, r: 2 };  // implicit wrap
    return eval(e);
}
```

Implementation is sugar over the existing enum machinery —
the checker desugars `type X = A | B | C` to a synthesised
`enum X { A(A), B(B), C(C) }` before the rest of the
pipeline runs. The implicit wrap lets bare struct literals
flow into union positions without `Member(...)` ceremony.

Punted to follow-ups (don't block self-hosting in practice):
- Generic union members (`type Tree[T] = Leaf[T] | Node[T]`)
- Qualified variant references (`Expr.Add(...)`)

**Minimal fix**: union-of-structs sugar over the existing
enum machinery. Each `type Expr = A | B | C` lowers to an
auto-generated enum with one variant per struct type, and
`match expr { A(a) => ..., B(b) => ... }` works as today.
Type-system extension; ~1-2 weeks design + implementation.

### Major gaps (workarounds heavy)

1. **Process spawning** — the compiler invokes `clang` /
   `lld` / `wasm-tools` (see Part 3). Lang has no `exec`
   and WASI's `wasi:system/process` proposal isn't
   finalised. **Path forward**: keep a thin Go bootstrap
   that handles linking only, and let the fern-port emit
   asm / WAT. Standard self-hosting pattern (TinyCC and
   others do this).

2. **No sort / no custom comparators**. Compiler sorts
   small lists (field offsets, map sizes). Inline the sort
   bodies or add `(arr: T[]).sort_by(cmp)` to the prelude
   once first-class function args to generic functions
   work cleanly.

### Minor gaps (workarounds cheap, ~50-200 lines each)

- **No `u64` Map keys** — only `i32` and `string` today.
  Re-key to strings or add a u64-keyed variant.
- **No char classifiers** (`is_digit`, `is_letter`,
  `is_hex`) — add to prelude.
- **No `os.Stat` equivalent** — use `read_file()`'s error
  arm to probe existence.
- **No sprintf-style formatter** — ~~concat with `+`, or
  add a minimal `format(fmt, args[])` to prelude.~~ Resolved:
  `format(fmt, args: string[])` landed in the prelude.
  Walks the template byte-by-byte and substitutes each `{}`
  placeholder with the next `args[i]`. Callers pre-stringify
  via `.to_string()`. Underflow (more `{}`s than args) emits
  literal `{}` so the bug surfaces at runtime; overflow
  (more args) silently drops extras. Mirrors Python
  `str.format()` / Rust `format!()` minimal subset.
- **No regex** — hand-coded byte scans (the prelude
  already does this in `trim` / `split` / etc.).

### Realistic first-port milestone

The lexer. ~400 Go LOC → ~600 lang LOC. Pure procedural,
produces simple Token structs, validates string indexing /
slicing / struct-lit creation under real workload.
**Estimated 1 week.**

Then parser (~2 weeks; validates recursive descent +
recursive struct types + pattern matching), then a stub
checker (~1 week; symbol-table over `Map[string, Symbol]`),
then IR emission (~3 weeks).

**Full lexer → IR on wasm**: 4-6 weeks. Union types (PRs
#390 + #392) landed the AST visitor pattern; closures on
every backend (already in place) cover the small visitor-
callback usage. Full compiler self-host on wasm: 6-9 weeks.
Native self-host (arm64-linux / x86-64) is now equally
viable since closures-with-captures lower on every target.

---

## Part 3 — Why we need clang, lld, and wasm-tools

The lang compiler emits **text** at every backend — `.s`
assembly for arm64-linux / arm64-darwin / x86-64-linux, and
`.wat` (WebAssembly Text Format) for wasm. Producing a
runnable binary from that text requires external tools.

### clang (or `aarch64-linux-gnu-gcc` / `x86_64-linux-gnu-gcc`)

**Used at**: `cmd/fern/main.go:311,347` via `exec.Command`.

The lang compiler does **not** include an assembler. After
emitting `.s` it shells out to a C toolchain driver, passing
flags like `-static -nostdlib -s prog.s -o binary`. Clang /
gcc:

1. Run the assembler (`as`) to turn `.s` into a relocatable
   object file. Reading text asm and emitting machine code
   for the target ISA is non-trivial — even a minimal
   assembler is a few thousand lines per architecture.
2. Run the linker to resolve symbols, lay out sections,
   write the ELF / Mach-O headers, and patch in start /
   exit code.
3. Drop libc, crt0, and libgcc with `-nostdlib` because the
   compiler provides its own `_start`, syscall wrappers,
   allocator, and string runtime in the prelude.

The choice between `clang` and the cross `*-linux-gnu-gcc`
toolchains is just driver preference — the actual work
(assembling + linking) is identical. clang is used for
arm64-darwin specifically because of point 2 below.

### lld (LLVM linker, Mach-O backend)

**Used at**: `cmd/fern/main.go:304` via `-fuse-ld=lld`.

Only relevant for **cross-compiling to arm64-darwin from
Linux**. On a native macOS host, clang's default linker is
ld64 (Apple's) and you don't need lld. On a Linux CI box
producing Mach-O binaries for the macOS CI runner, you
need a linker that understands Mach-O object files, dylib
linkage, and the dyld stub layout. ld64 isn't packaged for
Linux; lld is. The flag combination is:

```
clang --target=arm64-apple-darwin -fuse-ld=lld -nostdlib \
      -Wl,-arch,arm64 prog.s -o binary
```

On the native macOS path (`cmd/fern/main.go:300`) the
arguments are different — `-nostdlib -lSystem` — because
Apple's ld64 requires the libSystem.dylib stub for dynamic
linkage, even when we link nothing FROM libSystem.

### wasm-tools *(no longer a toolchain dependency)*

**Historical.** The wasm backend used to shell out to
`wasm-tools` in three subprocess calls (`parse` →
`component embed` → `component new --adapt
wasi_snapshot_preview1=ADAPTER`) to turn the emitted core
module into a **Component Model** artifact — the shape
edge-function runtimes (`wasmtime serve`, Fastly Compute,
Cloudflare workerd, etc.) consume.

That pipeline is gone. The "10K+ lines of work" this section
once argued against — a native binary wasm encoder, the
Component Type encoder, and the preview-1 → preview-2 adapter
— now lives in-tree under `internal/wasm/*` (`encode`,
`componenttype`, `component`, plus the `leb128` / `sections`
/ `inst` / `imports` / `memory` / … building blocks).
`fern -target wasm` / `-target wasi-http` compose components
natively in Go (`component.ClassifyCore` → `component.Compose`,
see `cmd/fern/main.go`), with no `wasm-tools` shell-out and no
preview-1 adapter. The only external process the toolchain
still spawns is the linker (and qemu under `--run`) — building
and running wasm output needs neither `wasm-tools` nor the
adapter. A handful of e2e tests still call `wasm-tools print`
to inspect a composed component, but that's a test-only
convenience, not a build dependency.

### Why a self-hosted compiler needs process spawning

To produce a binary, the compiler MUST invoke at least the
linker — symbol resolution + section layout + executable
header writing is too much code to bring into the language
runtime today. For wasm targets you additionally need the
component-model pipeline.

The standard self-hosting workaround is to keep a thin
external orchestrator that handles ONLY the link step:

```
[lang source] → [lang compiler in lang]
              → [.s text + temp file write]
              → [thin Go bootstrap calls clang / lld / wasm-tools]
              → [runnable binary]
```

The "compiler in lang" part covers lex / parse / check /
monomorph / closureconv / IR / codegen — ~95% of the
codebase by line count. The bootstrap stays in Go (or a
shell script) and is ~50 lines: read asm from a temp file,
exec clang, exit-code propagation. Many self-hosting
compilers settle here permanently (TinyCC, Roc, Zig's
self-host bootstrap before it got `std.process.spawn`).

If WASI ever lands a stable process-spawn API
(`wasi:system/process`, currently proposed but unstable)
the bootstrap could move into the lang runtime too. Until
then, the bootstrap is the pragmatic answer.
