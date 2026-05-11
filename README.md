# lang

A small statically-typed language with several backends, written in Go.
Targets so far:

- **ARM64 / aarch64 Darwin** Mach-O — native Apple Silicon Macs
  (Apple M-series); the **default** target. No Linux container
  required on a Mac; `clang` + `ld64` link directly. (For cross-
  compile from Linux, `lld`'s Mach-O backend is used instead.)
- **ARM64 / aarch64** Linux ELF — Raspberry Pi 4+, AWS Graviton,
  Android, qemu-aarch64 under test.
- **WebAssembly** — emitted as a WASI Preview 2 Component Model
  component, ready for `wasmtime run` (CLI) or `wasmtime serve`
  (`wasi:http/incoming-handler`).
- x86-64 is on the roadmap.

The language is a tiny TypeScript-flavoured subset: functions, methods
on structs, `var`, `if` / `else`, `while`, `for`, `switch`, ternaries,
numbers, booleans, floats, strings, arrays, structs, nested-function
closures, and the usual arithmetic / logical / bitwise operators.

The compiler is end-to-end:

```
source
  ──► lexer
  ──► parser            (recursive descent)
  ──► type checker      (errors aggregated, did-you-mean hints)
  ──► closure conversion (hoists nested functions, rewrites captures)
  ──► IR lowering       (structured stack-machine IR)
  ──► IR optimisation   (see "optimisation" below)
  ──► ARM64 emitter  ──► .s   (Mach-O / Apple Silicon, default;
      or                       or Linux ELF via `-target arm64`)
      WASM emitter   ──► .wat (preview-2 component via wasm-tools)
```

Both backends share the IR layer, so a new language feature usually
needs to grow `Lower` + the IR; codegen picks it up for free.

## Origin

This project was inspired by Vladimir Keleshev's book *Compiling to
Assembly from Scratch* (https://keleshev.com/compiling-to-assembly-from-scratch).
The book teaches an end-to-end pipeline against ARM32 using a
TypeScript subset. **No source from the book or its companion repo
was copied or translated.** The architecture here was designed
independently in idiomatic Go (recursive-descent parser,
interface-typed AST, type switches for visitors), then grew a
WebAssembly backend, an explicit IR, a small optimisation pipeline,
and a few language features the book doesn't cover. Read the book if
you want the theory; this repo is just one way to assemble those
ideas in Go.

## Build & run

```
go build ./cmd/lang

# ARM64 macOS (default target, Apple Silicon)
#   Run natively on a Mac with clang:
./lang -o factorial examples/factorial.lang
./factorial
#   ...or cross-compile from Linux with clang + lld (the binary
#   ships unchanged; copy to a Mac to run):
./lang -cc clang -o factorial examples/factorial.lang

# ARM64 Linux
./lang -target arm64 examples/factorial.lang > factorial.s
aarch64-linux-gnu-gcc -static -nostdlib factorial.s -o factorial
qemu-aarch64 factorial

# WASM (preview-2 component)
./lang -target wasm -wasi-adapter $LANG_WASI_ADAPTER \
    -o factorial.wasm examples/factorial.lang
wasmtime run factorial.wasm

# Formatter
./lang -fmt examples/factorial.lang        # writes idiomatic source to stdout
./lang -fmt -w examples/factorial.lang     # overwrite the file in place
./lang -fmt -d examples/factorial.lang     # print a unified diff against
                                           # the file; exits 1 when they differ
```

The formatter strips `//` line comments and blank lines because the
lexer drops both before they reach the AST — it's a re-emit from the
parsed tree, not a token-stream transform. Format → parse → format is
byte-stable.

`go test ./...` runs the unit tests and the IR-level pass tests. The
e2e tests in `internal/e2e` exercise the full pipeline on both
backends — linking arm64 assembly with `aarch64-linux-gnu-gcc` and
running it under `qemu-aarch64`, and running WAT through `wasmtime`.
Both suites skip automatically when the toolchains aren't on `PATH`.
CI installs all of them so the full pipeline is exercised on every
push. A separate macOS job (`.github/workflows/macos.yml`) verifies
the arm64-darwin Mach-O target natively on Apple Silicon.

The `Makefile` wraps the common flows:

```
make build           # go build → bin/lang
make test            # go test ./...
make examples        # compile + cross-link every examples/*.lang (arm64 Linux)
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

- **Modules / imports** — split a program across files via
  `import "./path";` at the top of the entry file. Imports resolve
  relative to the importing file's directory; `.lang` is appended
  automatically. Functions from `import "./util";` are addressed
  as `util.fn(args)`; struct types as `util.Foo` (in `var x: util.Foo`
  / `function f(): util.Foo` / `util.Foo { … }` literal). The loader
  detects cycles, mangles non-entry module names internally so the
  rest of the pipeline sees one flat program.
- **Visibility** — top-level decls are private to their module by
  default. Mark them `pub function …` / `pub struct …` /
  `pub const …` to expose them across module boundaries;
  cross-module references to non-`pub` decls fail at load time
  with a diagnostic that names the offending qualified reference.
- **Top-level constants** — `const NAME[: T] = expr;` declares a
  named constant. Initialisers may be literals or arithmetic /
  comparison / logical / string-concat expressions over earlier
  consts (`const PI: float = 3.14; const TWO_PI: float = PI * 2.0;`).
  References fold to literals at compile time; the checker / IR /
  codegen never see the const decl. `pub const` exports across
  modules just like `pub function`.
- Top-level `function` declarations with parameter and return types.
- **Sum types** via `enum Foo { Bar, Baz(T1, T2) }`. Variants are
  constructed by name (`Bar`, `Baz(1, 2)`) and consumed via
  `match (e) { Bar => { … }, Baz(a, b) => { … } }`. Match is
  exhaustiveness-checked: every variant must be covered or the
  arm list ends with `_`. Variant payloads bind into per-arm
  locals. Tagged-union values lower to a heap-allocated
  `[tag, payload0, …]` block on the bump heap.
- **Generic enums** via `enum Option[T] { Some(T), None }` and
  `enum Result[T, E] { Ok(T), Err(E) }`. Type arguments are
  inferred from constructor payload types (`Some(42)` →
  `Option[number]`); payload-less variants on generic enums
  (`None`) infer their type arguments from the surrounding
  context (var annotation, function return type). Generics are
  **erased** at runtime — payloads stay i32-uniform on the heap,
  so generics add zero per-instantiation codegen.
- **Methods** on structs via the receiver clause
  `function (p: Point) name(): T { ... }`; the checker rewrites call
  sites and lowers them as plain functions.
- **Nested functions** with closure-by-value over scalar outer-scope
  variables; closure conversion hoists them to the top level and
  threads a synthetic env parameter (`wasm` only).
- `var x: T = expr;` (annotation optional — inferred from the
  initialiser).
- Statements: `if` / `else`, `while`, `for(init; cond; step)`,
  `for x in arr / "string"` (desugars to an index loop at parse
  time), `switch` (with comma-separated case values, `default`),
  `return`, `break`, `continue`, blocks, expression statements.
- Types: `number` (32-bit signed), `boolean`, `void`, `float` (32-bit
  IEEE — single-precision FPU on ARM64, `f32` on WASM), `string`,
  arrays (`number[]`), nominal struct types, and function types
  (`(T, U) => V`).
- Operators: `+ - * / %`, `== != < > <= >=`, `&& || !`, bitwise
  `& | ^ << >>`, unary `-`. String `+` concatenates, string `==` /
  `!=` compare contents, string indexing returns the byte at that
  position.
- Literals: number, boolean, float (e.g. `1.5`), string, arrays, and
  struct constructors (`Point { x: 3, y: 4 }`).
- `len(s)` / `len(arr)` — single-load lookup of the 4-byte length
  prefix every heap-resident sequence carries.
- Compound assignment (`x += 7` desugars to `x = x + 7`).
- Ternary `cond ? then : else`.
- Tail-call optimisation: a `return self(...)` rewrites to a loop
  back-edge so deep recursion stays O(1) in stack depth.
- Function values: `var f = add; return f(40, 2);` lowers to an
  indirect call.

Built-ins:

- `print(s: string): void` — newline-terminating output (direct
  `write(2)` syscall on ARM64, WASI `fd_write` on WASM).
- `write(s: string): void` — stdout without a trailing newline.
  Use to compose your own output formatting (status lines, prompts,
  custom delimiters).
- `eprint(s: string): void` — `print` shape but routed to stderr,
  so error / diagnostic output stays out of the stdout pipe.
- `putchar(n: number): void` — writes one byte.
- `len(x): number` — array or string length.
- `args(): string[]` — command-line arguments. argv[0] is the
  program / module path (matching C and Go conventions); the rest
  are the user-supplied positional args. The first call materialises
  a length-prefixed string array from libc / WASI; subsequent calls
  return the cached pointer.
- `stdin(): Reader` / `stdout(): Writer` / `stderr(): Writer`
  — the standard streams as `Reader` / `Writer` values. Use
  `stdin().read_line()` for line-by-line input;
  `stdout().write(s)` / `stderr().write(s)` mirror the file
  streaming methods.
- `env(name: string): Option[string]` — environment variable
  lookup. `Some(value)` for a present key (including
  explicitly-empty values); `None` for missing.
- `exit(code: number): void` — terminates the process with the
  given status. Useful for `eprint(msg); exit(2)` failure paths;
  the success path can just `return` from main.
- `read_file(path): Result[string, IoError]` — slurps the entire
  file into a string. `Ok(content)` on success; `Err(IoError)`
  with the path attached on failure.
- `write_file(path, content): Option[IoError]` — truncates and
  writes. `None` for success, `Some(err)` for failure.
- `open_reader(path): Result[Reader, IoError]` — opens for
  reading and returns a `Reader` value with `.read_line()`,
  `.read_chunk(size)`, and `.close()` methods. Use this for
  files large enough that slurping into a single string is
  wasteful, or for line-by-line filters.
- `open_writer(path): Result[Writer, IoError]` — opens for
  writing (truncates the file). `Writer` has `.write(s)` and
  `.close()` methods.
- `open_appender(path): Result[Writer, IoError]` — same as
  `open_writer` but preserves existing content; writes go
  to the end of the file.

WASM builds need a preopened directory — pass `wasmtime --dir=...`
when running, and paths are interpreted relative to that preopen
(absolute paths fail with `Other`).

`Option[T]`, `Result[T, E]`, and `IoError` are built into the
language — they're auto-injected as enums on every program with
the canonical Rust-shaped variants (`Some(T) / None`,
`Ok(T) / Err(E)`). `IoError` carries the offending path on
variants where it makes sense (`NotFound(path)`,
`PermissionDenied(path)`, `AlreadyExists(path)`,
`InvalidUtf8(path)`, plus payload-less `Interrupted` /
`Unsupported`, and the catch-all `Other(path, message)`).
Use them anywhere user-defined enums work, including in your
own function signatures.

## Optimisation

The IR is a stack-machine bytecode with structured control flow
(`block` / `loop` / `if` / `br` / `br_if`). Every backend consumes
the same `ir.Program`, so the optimisation pipeline lives in one
place and benefits both:

| Pass               | What it does |
|--------------------|--------------|
| `Inline`           | Substitutes small leaf-function bodies, including ones with internal control flow / multiple returns. |
| `FuseTee`          | Collapses adjacent `OpStoreLocal X ; OpLoadLocal X` to a single `OpTeeLocal X` (cleaner WAT, identity on ARM64). |
| `FlattenBranches`  | `if (c) { return X; } return Y;` → typed value-returning if + one trailing return. |
| `OptimizeCleanup`  | Iterates `PropagateCopies` (drop dead tees / stores) + `ConstPropagate` (replace loads of constant-bound slots) + `Fold` (constant arithmetic, constant-if pruning, const+drop) + `ReduceStrength` (`x * 2^k → x << k`, identity ops) to a fixed point. |
| `EliminateDeadCode`| Drops ops between a terminator (`OpReturn` / `OpReturnVoid` / `OpBr`) and the next control-flow merge. |

Concrete payoff — `function f(): number { var x: number = 7; var y:
number = x + 3; return y * 2 + x; }` lowers to twelve IR ops and
collapses to a single `const.i32 27 ; return` after the pipeline.

## Calling conventions

**ARM64**: standard AAPCS64, but the binary is libc-free. We link
with `gcc -static -nostdlib` (Linux) or `clang -nostdlib` (Darwin
Mach-O), emit our own `_start` — on Linux captures argc / argv /
envp from the kernel's initial stack into .bss globals; on Darwin
LC_MAIN delivers them in x0 / x1 / x2 — aligns sp, initialises
the bump heap, then calls `main`. Every I/O operation bottoms out
in a direct `svc #0` (Linux: number in x8) or `svc #0x80`
(Darwin: number in x16) syscall (`read` / `write` / `writev` /
`open` / `close` / `mmap` / `exit_group` / `getentropy` etc.).
Args 0..7 in x0..x7; extras from the caller's stack at
`fp+16`, `fp+24`, … The operand stack is simulated on the
physical sp via paired `str x0, [sp, #-16]!` / `ldr x0, [sp],
#16` push/pop; binary operators pop right into x0 and left into
x1. Heap-backed values (arrays, strings, structs) come from
`__lang_alloc`, a bump arena over a 64 MiB anonymous mmap region
reserved at startup; the arena's fast path is six instructions
plus a branch — pure in-process pointer bump, no syscall, no
per-allocation header, no individual `free`. Strings carry a
4-byte little-endian length prefix at `ptr - 4` (with a trailing
NUL preserved so our own `__lang_strcmp` / `__lang_memcpy` /
`__lang_strlen` keep working on the same data pointer). Integer
division / modulo lower to inline `sdiv` / `msub`. Float
operations use the AAPCS64 FP registers — the emitter keeps f32
bit patterns flowing through the integer operand stack and
`fmov`s them into single-precision s-registers just for
`fadd s0, s1, s2` / `fcmp s0, s1` / etc.

**WASM**: standard WASM calling convention. A `funcref` table holds
every function referenced as a value (or hoisted by closure
conversion); other functions stay outside the table so wasmtime's
`--invoke main` keeps working. Closures are `{fn_idx, env_ptr}`
8-byte heap pairs; arrays / strings / structs share the same
length-prefixed bump-allocated layout as ARM64.

## Repository layout

```
cmd/lang/                  # CLI driver
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
