# Fern

📚 **Documentation: <https://jakechampion.github.io/lang/>**
([tutorial](https://jakechampion.github.io/lang/tutorial/install/) ·
[reference](https://jakechampion.github.io/lang/reference/syntax/) ·
[standard library](https://jakechampion.github.io/lang/stdlib/) ·
[playground](https://jakechampion.github.io/lang/playground/))

Fern is a small statically-typed language with several backends, written
in Go, built for fast-startup CLI tools and short-lived edge-function HTTP
servers. Targets so far:

- **ARM64 / aarch64** Linux ELF — the **default** target (Raspberry Pi 4+,
  AWS Graviton, Android, qemu-aarch64). Assembled and linked **in-process**
  by the pure-Go native backend — no external toolchain needed. Pass `-cc
  aarch64-linux-gnu-gcc` to opt out to an external assembler/linker.
- **ARM64 / aarch64 Darwin** Mach-O — native Apple Silicon Macs via `clang`
  + `ld64` (or `lld` when cross-compiling from Linux).
- **x86-64 / amd64** Linux ELF — System V AMD64 ABI. Like arm64, assembled
  and linked **in-process** by the pure-Go native backend (no external
  toolchain); pass `-cc x86_64-linux-gnu-gcc` to opt out.
- **WebAssembly** — a WASI Preview 2 Component Model component, ready for
  `wasmtime run` or `wasmtime serve` (`wasi:http/incoming-handler`).

The pipeline is end-to-end — lexer → recursive-descent parser → type checker
(aggregated errors, did-you-mean hints) → closure conversion → IR lowering →
IR optimisation → ARM64 (`.s`, Linux ELF or Mach-O via `-target arm64-darwin`)
or WASM (`.wat` → preview-2 component) emitter. Both backends share the IR
layer, so a new language feature usually needs only `Lower` + the IR; codegen
picks it up for free.

Inspired by Vladimir Keleshev's *Compiling to Assembly from Scratch*
(https://keleshev.com/compiling-to-assembly-from-scratch), but designed
independently in idiomatic Go — no source from the book was copied.

## Build & run

```
go build ./cmd/fern

# ARM64 Linux (default target)
./fern examples/factorial.fern > factorial.s
aarch64-linux-gnu-gcc -static -nostdlib factorial.s -o factorial
qemu-aarch64 factorial

# ARM64 macOS (Apple Silicon)
#   Run natively on a Mac with clang:
./fern -target arm64-darwin -o factorial examples/factorial.fern
./factorial
#   ...or cross-compile from Linux with clang + lld (the binary
#   ships unchanged; copy to a Mac to run):
./fern -target arm64-darwin -cc clang -o factorial examples/factorial.fern

# WASM (self-contained preview-2 component, no external adapter)
./fern -target wasm-bin -component-wrap -o factorial.wasm examples/factorial.fern
wasmtime run --invoke 'main()' factorial.wasm   # prints 720

# Formatter
./fern -fmt examples/factorial.fern        # writes idiomatic source to stdout
./fern -fmt -w examples/factorial.fern     # overwrite the file in place
./fern -fmt -d examples/factorial.fern     # print a unified diff against
                                           # the file; exits 1 when they differ
```

The formatter re-emits from the parsed tree, so `//` comments and blank lines
are dropped; format → parse → format is byte-stable.

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
struct Point { x: number, y: number }

function (p: Point) magnitude(): number {
  return p.x * p.x + p.y * p.y;
}

function factorial(n: number, acc: number): number {
  if (n == 0) { return acc; }
  return factorial(n - 1, acc * n);    // tail call → loop
}

function main(): number {
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
- **Nested functions** with closure-by-value over scalar outer-scope variables.
- `var x: T = expr;` (annotation optional — inferred from the initialiser).
- Statements: `if` / `else`, `while`, `for(init; cond; step)`,
  `for x in arr / "string"`, `switch` (comma-separated cases, `default`),
  `return`, `break`, `continue`, blocks, expression statements.
- Types: `number` (32-bit signed), `boolean`, `void`, `float` (32-bit IEEE),
  `string`, arrays (`number[]`), nominal structs, function types
  (`(T, U) => V`).
- Operators: `+ - * / %`, `== != < > <= >=`, `&& || !`, bitwise
  `& | ^ << >>`, unary `-`. String `+` concatenates, `==` / `!=` compare
  contents, indexing returns the byte at a position.
- Literals: number, boolean, float, string, arrays, struct constructors.
- `len(s)` / `len(arr)`, compound assignment (`x += 7`), ternary
  `cond ? then : else`, tail-call optimisation, and function values
  (lowered to indirect calls).

Built-ins:

- `print` / `write` / `eprint` / `putchar` — output (stdout newline-terminated,
  stdout raw, stderr, single byte).
- `len(x): number`, `args(): string[]`, `exit(code): void`.
- `stdin(): Reader` / `stdout(): Writer` / `stderr(): Writer` — standard
  streams with `.read_line()` / `.write(s)` methods.
- `env(name): Option[string]` — environment lookup.
- `read_file` / `write_file` — slurp / truncate-write whole files.
- `open_reader` / `open_writer` / `open_appender` —
  `Result[Reader|Writer, IoError]` with `.read_line()` / `.read_chunk(size)` /
  `.write(s)` / `.close()` for streaming.

WASM builds need a preopened directory — pass `wasmtime --dir=...`; paths are
relative to that preopen.

`Option[T]`, `Result[T, E]`, and `IoError` are built in, auto-injected as
enums on every program with the canonical Rust-shaped variants. `IoError`
carries the offending path where it makes sense (`NotFound(path)`,
`PermissionDenied(path)`, `Other(path, message)`, etc.). Use them anywhere
user-defined enums work.

## Optimisation

The IR is a stack-machine bytecode with structured control flow. Every backend
consumes the same `ir.Program`, so the optimisation pipeline lives in one place:

| Pass               | What it does |
|--------------------|--------------|
| `Inline`           | Substitutes small leaf-function bodies, including ones with internal control flow / multiple returns. |
| `FuseTee`          | Collapses adjacent `OpStoreLocal X ; OpLoadLocal X` to a single `OpTeeLocal X` (cleaner WAT, identity on ARM64). |
| `TailCallOptimize` | Wraps the body in a loop and rewrites `OpCallDirect <self> ; OpReturn` to a parameter rebind plus `OpBr`. Currently unwired (the arm64 backend doesn't call it); kept for the upcoming x86-64 backend. |
| `FlattenBranches`  | `if (c) { return X; } return Y;` → typed value-returning if + one trailing return. |
| `OptimizeCleanup`  | Iterates `PropagateCopies` (drop dead tees / stores) + `ConstPropagate` (replace loads of constant-bound slots) + `Fold` (constant arithmetic, constant-if pruning, const+drop) + `ReduceStrength` (`x * 2^k → x << k`, identity ops) to a fixed point. |
| `EliminateDeadCode`| Drops ops between a terminator (`OpReturn` / `OpReturnVoid` / `OpBr`) and the next control-flow merge. |

Concrete payoff — `function f(): number { var x: number = 7; var y:
number = x + 3; return y * 2 + x; }` lowers to twelve IR ops and
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
internal/codegen/          # ARM64 emitter (arm64/) + WASM emitter (wasm/)
internal/diag/             # error formatting with source context
internal/e2e/              # end-to-end tests for both backends
internal/interp/           # AST tree-walking interpreter (REPL)
examples/                  # sample programs
```
