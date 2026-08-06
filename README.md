# Fern

📚 **Documentation: <https://jakechampion.github.io/lang/>**
([tutorial](https://jakechampion.github.io/lang/tutorial/install/) ·
[reference](https://jakechampion.github.io/lang/reference/syntax/) ·
[standard library](https://jakechampion.github.io/lang/stdlib/) ·
[playground](https://jakechampion.github.io/lang/playground/))

Fern is a small statically-typed, general-purpose language with several
backends, written in Go. It grew up around two workloads it's especially
good at — fast-startup CLI tools and short-lived edge-function HTTP
servers — and is broadening out from there into a language you can reach
for generally, including long-running programs (its own self-hosted
compiler among them). Targets so far:

- **ARM64 / aarch64** Linux ELF — the **default** target (Raspberry Pi 4+,
  AWS Graviton, Android, qemu-aarch64). Assembled and linked **in-process**
  by the pure-Go native backend — no external toolchain needed. Pass `-cc
  aarch64-linux-gnu-gcc` to opt out to an external assembler/linker.
- **ARM64 / aarch64 Darwin** Mach-O — native Apple Silicon Macs. Assembled,
  linked, and **ad-hoc code-signed in-process** by the pure-Go native
  backend (static, no dyld) — no external toolchain needed. Pass `-cc clang`
  to opt out to clang + `ld64`/`lld`.
- **x86-64 / amd64** Linux ELF — System V AMD64 ABI, Haswell-class baseline
  (2013: SSE4.2 + BMI1). Like arm64, assembled and linked **in-process** by
  the pure-Go native backend (no external toolchain); pass `-cc
  x86_64-linux-gnu-gcc` to opt out.
- **WebAssembly** — a WASI Preview 2 Component Model component, ready for
  `wasmtime run` or `wasmtime serve` (`wasi:http/incoming-handler`).

The pipeline is end-to-end — lexer → recursive-descent parser → type checker
(aggregated errors, did-you-mean hints) → monomorphisation → closure
conversion → IR lowering → IR optimisation → backend emitter: ARM64 (`.s`,
Linux ELF or Mach-O via `-target arm64-darwin`), x86-64 (`.s`, Linux ELF), or
WASM (preview-2 component). The native backends share the IR layer, so a new
language feature usually needs only `Lower` + the IR; codegen picks it up for
free.

Inspired by Vladimir Keleshev's *Compiling to Assembly from Scratch*
(https://keleshev.com/compiling-to-assembly-from-scratch), but designed
independently in idiomatic Go — no source from the book was copied.

## Install

Three ways to get `fern`, easiest first (see the
[install guide](https://jakechampion.github.io/lang/tutorial/install/)
for details):

```
# 1. Prebuilt binary — grab the asset for your platform from the rolling
#    nightly release: https://github.com/JakeChampion/lang/releases/tag/nightly
#    (fern-linux-x86_64 / fern-linux-arm64 / fern-darwin-arm64).tar.gz

# 2. go install (needs Go 1.24+)
go install github.com/jakechampion/lang/cmd/fern@latest

# 3. Build from a checkout
go build ./cmd/fern
```

## Build & run

The native backends assemble **and** link in-process, so producing an
executable needs no external toolchain:

```
# ARM64 Linux (the default target)
./fern -o factorial examples/factorial.fern
qemu-aarch64 factorial          # or run natively on arm64 hardware

# ARM64 macOS (Apple Silicon) — runs natively on a Mac
./fern -target arm64-darwin -o factorial examples/factorial.fern
./factorial
#   ...or cross-compile from Linux (the binary ships unchanged; copy to a Mac):
./fern -target arm64-darwin -cc clang -o factorial examples/factorial.fern

# x86-64 Linux
./fern -target x86-64 -o factorial examples/factorial.fern
./factorial

# WASM (self-contained preview-2 component, no external adapter)
./fern -target wasm -o factorial.wasm examples/factorial.fern
wasmtime run factorial.wasm

# Run straight through the interpreter (no binary emitted)
./fern -interp examples/factorial.fern

# Formatter
./fern -fmt examples/factorial.fern        # writes idiomatic source to stdout
./fern -fmt -w examples/factorial.fern     # overwrite the file in place
./fern -fmt -d examples/factorial.fern     # print a unified diff against
                                           # the file; exits 1 when they differ

# Per-package capability report (net / fs / env / subprocess / time / random;
# see docs/PACKAGE-CAPABILITIES-BRIEF.md). Grants are also ENFORCED on every
# compile / -check / -interp: a dependency whose fern.toml entry carries
# `capabilities = [...]` gets an E070 error when it reaches outside the grant
# (dependencies without the key warn for now).
./fern -capabilities app/main.fern

# Literate programming (Knuth-style named chunks; see docs/LITERATE.md)
./fern -interp examples/literate/fizzbuzz.fern.md   # tangle in memory, then run
./fern -tangle examples/literate/fizzbuzz.fern.md   # emit plain Fern source
./fern -weave  examples/literate/fizzbuzz.fern.md   # emit cross-referenced Markdown
```

To opt out to an external assembler/linker, pass `-cc` (e.g. `-cc
aarch64-linux-gnu-gcc` on Linux, `-cc clang` on Darwin).

The formatter re-emits from the parsed tree, so `//` comments and blank lines
are dropped; format → parse → format is byte-stable.

A `.fern.md` file is a Markdown document whose `fern` code chunks (`<<name>>=`)
are reassembled — *tangled* — from the root chunk `<<*>>` into a compilable
program; chunks may be defined in any order. A literate file works anywhere a
`.fern` file does (compile / `--run` / `-check` / `-interp`): it's tangled in
memory first, and diagnostics are mapped back to the line you wrote in the
document. See [docs/LITERATE.md](docs/LITERATE.md).

`go test ./...` runs the unit and IR-pass tests. The e2e tests in
`internal/e2e` exercise the full pipeline on both backends (linking arm64 with
`aarch64-linux-gnu-gcc` under `qemu-aarch64`, running WAT through `wasmtime`),
skipping automatically when toolchains aren't on `PATH`. CI installs all of
them; a separate macOS job (`.github/workflows/macos.yml`) verifies the
arm64-darwin Mach-O target natively on Apple Silicon.

The `Makefile` wraps the common flows:

```
make build           # go build → bin/fern
make test            # go test ./...
make examples        # compile + cross-link every examples/*.fern (arm64 Linux)
make run-factorial   # compile, link, run under qemu-aarch64
```

## Language at a glance

```
struct Point { x: i32, y: i32 }

function (p: Point) magnitude(): i32 {
  return p.x * p.x + p.y * p.y;
}

function factorial(n: i32, acc: i32): i32 {
  if (n == 0) { return acc; }
  return factorial(n - 1, acc * n);    // tail call → loop
}

function main(): i32 {
  var origin: Point = Point { x: 3, y: 4 };
  print("hello");                         // write(2) syscall on arm64, fd_write on wasm
  return origin.magnitude() + factorial(5, 1);
}
```

Supported:

- **Modules / imports** via `import "./path";` — resolved relative to the
  importing file, `.fern` appended; functions addressed as `util.fn(args)`,
  struct types as `util.Foo`. The loader detects cycles and flattens to one
  program.
- **Visibility** — top-level decls are module-private by default; mark them
  `pub function` / `pub struct` / `pub const` to export.
- **Top-level constants** — `const NAME[: T] = expr;`, where initialisers may
  be expressions over earlier consts; references fold to literals at compile
  time.
- Top-level `function` declarations with typed parameters and return.
- **Sum types** via `enum Foo { Bar, Baz(T1, T2) }`, consumed with
  exhaustiveness-checked `match`; values lower to a heap `[tag, payload…]`
  block.
- **Generic enums** (`enum Option[T] { Some(T), None }`,
  `Result[T, E] { Ok(T), Err(E) }`) — type arguments inferred, generics
  erased at runtime.
- **Methods** on structs via the `function (p: Point) name(): T` receiver
  clause.
- **Nested functions** with closures that capture outer-scope
  variables by value — scalars and pointer-shaped values (strings,
  arrays, structs) alike. Reference-typed captures are read-only
  inside the closure (reassigning one is rejected, since it could
  close a reference cycle); return the new value instead.
- `var x: T = expr;` (annotation optional — inferred from the initialiser).
- Statements: `if` / `else`, `while`, `for(init; cond; step)`,
  `for x in arr / "string"`, `match` (pattern dispatch, incl. literal
  arms), `return`, `break`, `continue`, blocks, expression statements.
- Types: sized integers `i8` / `i16` / `i32` / `i64` / `u8` / `u16` /
  `u32` / `u64` plus `usize` (target-aware native pointer width; `i32`
  is the default literal type), `boolean`, `void`, `f32` / `f64`
  (IEEE), `string`, owned arrays (`i32[]`), non-owning slice views
  (`[i32]`), tuples (`(i32, string)`), `Map[K, V]`, nominal structs,
  generic structs/enums, and function types (`(T, U) => V`).
- Operators: `+ - * / %`, `== != < > <= >=`, `&& || !`, bitwise
  `& | ^ << >>`, unary `-`. String `+` concatenates, `==` / `!=` compare
  contents, indexing returns the byte at a position.
- Literals: integer, boolean, float, string, arrays, struct constructors.
- `len(s)` / `len(arr)`, compound assignment (`x += 7`), `if` /
  `match` as expressions (`var s = if (x > 0) { "+" } else { "-" };`),
  tail-call optimisation, and function values (lowered to indirect
  calls).

Built-ins:

- `print` / `write` / `eprint` / `putchar` — output (stdout newline-terminated,
  stdout raw, stderr, single byte).
- `len(x): i32`, `args(): string[]`, `exit(code): void`.
- `stdin(): Reader` / `stdout(): Writer` / `stderr(): Writer` — standard
  streams with `.read_line()` / `.write(s)` methods.
- `env(name): Option[string]` — environment lookup.
- `read_file` / `write_file` — slurp / truncate-write whole files.
- `open_reader` / `open_writer` / `open_appender` —
  `Result[Reader|Writer, IoError]` with `.read_line()` / `.read_chunk(size)` /
  `.write(s)` / `.close()` for streaming.

WASM builds need a preopened directory — pass `wasmtime --dir=...`; paths are
relative to that preopen.

`Option[T]`, `Result[T, E]`, and `IoError` are built into the language —
always in scope, no import needed — as enums with the canonical
Rust-shaped variants. `IoError` carries the offending path where it makes
sense (`NotFound(path)`, `PermissionDenied(path)`, `Other(path, message)`,
etc.). Use them anywhere user-defined enums work. See the
[error-handling reference](https://jakechampion.github.io/lang/reference/error-handling/)
for the `?` operator and the combinator methods.

## Optimisation

The IR is a stack-machine bytecode with structured control flow. Every backend
consumes the same `ir.Program`, so the optimisation pipeline lives in one place:

| Pass               | What it does |
|--------------------|--------------|
| `Inline`           | Substitutes small leaf-function bodies, including ones with internal control flow / multiple returns. |
| `FuseTee`          | Collapses adjacent `OpStoreLocal X ; OpLoadLocal X` to a single `OpTeeLocal X` (cleaner WAT, identity on ARM64). |
| `TailCallOptimize` | Wraps the body in a loop and rewrites `OpCallDirect <self> ; OpReturn` to a parameter rebind plus `OpBr`. Wired into every backend (arm64, x86-64, wasm), so self-tail recursion runs in O(1) stack depth everywhere. |
| `FlattenBranches`  | `if (c) { return X; } return Y;` → typed value-returning if + one trailing return. |
| `OptimizeCleanup`  | Iterates `PropagateCopies` (drop dead tees / stores) + `ConstPropagate` (replace loads of constant-bound slots) + `Fold` (constant arithmetic, constant-if pruning, const+drop) + `ReduceStrength` (`x * 2^k → x << k`, identity ops) to a fixed point. |
| `EliminateDeadCode`| Drops ops between a terminator (`OpReturn` / `OpReturnVoid` / `OpBr`) and the next control-flow merge. |

Concrete payoff — `function f(): i32 { var x: i32 = 7; var y:
i32 = x + 3; return y * 2 + x; }` lowers to twelve IR ops and
collapses to a single `const.i32 27 ; return` after the pipeline.

## Calling conventions

**ARM64**: standard AAPCS64, libc-free — linked in-process by the native
backend on Linux (or `gcc -static -nostdlib` via `-cc`; `clang -nostdlib`
on Darwin), with our own `_start` that sets up
argc/argv/envp and the bump heap before calling `main`. I/O bottoms out in
direct syscalls. Heap-backed values come from `__fern_alloc`, a bump arena
over a 64 MiB mmap region with no per-allocation header and no `free`; strings
carry a 4-byte little-endian length prefix at `ptr - 4` (plus a trailing NUL).

**WASM**: standard WASM calling convention. A `funcref` table holds every
function referenced as a value; closures are `{fn_idx, env_ptr}` 8-byte heap
pairs, and arrays / strings / structs share the same length-prefixed
bump-allocated layout as ARM64.

## Repository layout

```
cmd/fern/                  # CLI driver
internal/lexer/            # token stream
internal/parser/           # recursive-descent parser → AST
internal/ast/              # AST types + Position
internal/checker/          # type checker + did-you-mean hints
internal/closureconv/      # nested-function hoisting
internal/ir/               # stack-machine IR + lowering + opt passes
internal/codegen/          # arm64/, x86_64/, wasmbin/ emitters
internal/native/           # pure-Go assemblers + ELF/Mach-O linkers
internal/monomorph/        # generic instantiation
internal/modload/          # module/import resolution
internal/diag/             # error formatting with source context
internal/e2e/              # end-to-end tests for every backend
internal/interp/           # AST tree-walking interpreter (REPL)
examples/                  # sample programs
```
