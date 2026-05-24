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

### 3. Wide-scalar Map K/V (i64 / u64 / f64) silently
   diverges across backends

- **Where**: `internal/ir/ir.go` `mapKeyKindTag` /
  `mapValKindTag` (only know 0=i32-scalar / 1=string /
  pointer); prelude's entry stride hardcoded to
  `2 * __ptr_width()`.
- **Why**: natives have 8-byte operand-stack slots so
  `Map[i64, ...]` accidentally works; wasm32 strictly
  validates `i32` and rejects the same calls. Code that
  passes CI on native fails at runtime / link time on wasm.
- **Fix**: extend the runtime-tag scheme to widths (or land
  Map specialization, which makes the per-instantiation
  entry-stride choice compile-time). Shares scope with the
  `usize` work in item 1.

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
  is straightforward on both architectures; the alignment
  requirement only matters at call boundaries.
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

### wasm-tools

**Used at**: `cmd/fern/main.go:381-415` (three subprocess
calls in sequence).

The wasm backend's job is to emit a **Component Model**
artifact, not a bare core module — that's what edge-function
runtimes (`wasmtime serve`, Fastly Compute, Cloudflare
workerd, etc.) consume. The pipeline:

1. **`wasm-tools parse`** — lowers our text WAT into a
   binary core wasm module (the `.wasm` you'd run with
   `wasmtime run`). Same job as `wat2wasm`.
2. **`wasm-tools component embed`** — annotates the binary
   module with a Component Type section, derived from the
   `.wit` interface description (embedded in the lang binary
   via Go's `embed` — see `cmd/fern/main.go:355-362`). This
   tells downstream tooling "this module implements the
   `lang` world / the `http` world".
3. **`wasm-tools component new --adapt
   wasi_snapshot_preview1=ADAPTER`** — wraps the annotated
   core module into a real Component Model component,
   adapting any preview-1 imports the module makes (via the
   wasi-preview2 adapter shim) into preview-2 imports. The
   result is a `.component.wasm` file that any preview-2
   host can load.

Without wasm-tools, the wasm target would stop at
`prog.wat`. Writing the parse + component-embed pipeline
ourselves would mean re-implementing the binary wasm
encoder, the Component Type encoder, and the
preview1-to-preview2 adapter — easily 10K+ lines of work,
and the format is still evolving (the Component Model
proposal isn't finalised). Shelling out to the upstream
tool is the standard practice; every wasm component
producer (rust-cargo-component, JCO, py2wasm, …) does this.

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
