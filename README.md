# Fern

Fern is a small, statically typed, general-purpose language that compiles to
a static native binary or to WebAssembly from the same source. It has no
runtime to boot and no garbage collector: memory is reference counted, with
the counting largely elided at compile time. A hello-world binary is a few
kilobytes and startup is the kernel's `exec` followed by your `main`.

The compiler is a single binary. It parses, type-checks, optimises,
assembles, and links in-process, so building a program needs nothing on
`PATH` but `fern` itself. The same binary formats, lints, type-checks,
interprets, resolves packages, and reports what capabilities a package
reaches.

**Documentation:** <https://jakechampion.github.io/lang/>
([tutorial](https://jakechampion.github.io/lang/tutorial/install/) ·
[reference](https://jakechampion.github.io/lang/reference/syntax/) ·
[standard library](https://jakechampion.github.io/lang/stdlib/) ·
[playground](https://jakechampion.github.io/lang/playground/) ·
[why Fern](https://jakechampion.github.io/lang/why/))

## A taste

```fern
import "std/string";
import "std/i32";

enum Shape { Circle(i32), Rect(i32, i32) }

function (s: Shape) area(): i32 {
  match (s) {
    Circle(r) => { return 3 * r * r; },
    Rect(w, h) => { return w * h; },
  }
}

// `?` unwraps Some and returns None early.
function parse_pair(a: string, b: string): Option[i32] {
  let w: i32 = a.parse_int()?;
  let h: i32 = b.parse_int()?;
  return Some(Rect(w, h).area());
}

function main(): i32 {
  let shapes: Shape[] = [Circle(2), Rect(3, 4)];
  let total: i32 = 0;
  for s in shapes {
    total = total + s.area();
  }
  print(total.to_string());               // 24
  match (parse_pair("6", "7")) {
    Some(n) => { print(n.to_string()); }, // 42
    None => { print("bad input"); },
  }
  return 0;
}
```

Sum types with exhaustive `match`, generics, traits with static dispatch,
methods on any type including primitives, closures, `Option` / `Result`
with `?`, `defer` / `errdefer`, saturating and checked integer operators,
and a module system with `pub` visibility. There are no exceptions and no
null. The [language reference](https://jakechampion.github.io/lang/reference/syntax/)
has the full surface.

## Install

```sh
# Prebuilt binary from the rolling nightly release
#   https://github.com/JakeChampion/lang/releases/tag/nightly
#   fern-linux-x86_64.tar.gz / fern-linux-arm64.tar.gz / fern-darwin-arm64.tar.gz

# Or with Go 1.26+
go install github.com/jakechampion/lang/cmd/fern@latest

# Or from a checkout
make build        # -> bin/fern
```

`fern -version` prints the commit a binary was built from.

## Build and run

```sh
fern -o hello examples/hello.fern                          # ARM64 Linux (default)
fern -target arm64-darwin -o hello examples/hello.fern     # Apple Silicon macOS
fern -target x86-64-linux -o hello examples/hello.fern     # x86-64 Linux
fern -target wasm32-wasi -o hello.wasm examples/hello.fern # WASI component
wasmtime run hello.wasm

fern -interp examples/hello.fern     # run through the interpreter, no binary
fern -check examples/hello.fern      # type-check only
fern -fmt -w examples/hello.fern     # format in place (-d for a diff)
fern -lint examples/                 # lint a tree (fern -lint-rules lists the rules)
fern -repl                           # interactive session
```

A `-target` compile runs the compiler, which assembles, links and
(for Darwin) code-signs in-process with no external toolchain. `fern` uses
`$FERN_SELFHOST` when set, else a `fern-selfhost` beside it (`make bootstrap`
installs one), else it builds one on first use from the compiler sources it
embeds, with the pinned stage0 compiler it downloads and verifies
(`docs/BOOTSTRAP.md`). The result is cached. `fern -targets` lists every target with the
capabilities its host provides; `fern -explain E030` explains an error
code.

### Targets

| Target | Output | Baseline |
| --- | --- | --- |
| `arm64-linux` (default) | static ELF | ARMv8.2-A with the crypto extensions |
| `arm64-android` | static position-independent ELF | as `arm64-linux` |
| `arm64-darwin` | static Mach-O, signed | Apple Silicon, latest macOS |
| `x86-64-linux` | static ELF | x86-64-v3 (Haswell, 2013) |
| `wasm32-wasi` | WASI Preview 2 component | `wasmtime run` |
| `wasm32-wasi-http` | `wasi:http/incoming-handler` component | `wasmtime serve` |

The CPU baselines are deliberate: a binary is static with no runtime
dispatch, so a selected instruction is a hard requirement. The
`-backend ssa` register-allocating emitter is available on both native Linux
ISAs. Per-backend support and known gaps: `docs/BACKEND-PARITY.md`.

## More than a compiler

- **Packages.** A `fern.toml` manifest declares `path`, hash-addressed `url`,
  and workspace dependencies; `fern -add`, `-fetch`, `-resolve` (Minimum
  Version Selection into `fern.lock`), and `-vendor` manage them. Only
  `-fetch` touches the network. `docs/PACKAGES.md`.
- **Capabilities.** A manifest can grant a dependency `net`, `fs`, `env`,
  `subprocess`, `time`, or `random`; reaching outside the grant is a compile
  error. `fern -capabilities` reports what each package uses and
  `fern -effects` does the same per function. `docs/PACKAGE-CAPABILITIES-BRIEF.md`.
- **Testing.** `std/test` is a TAP-13 runner written in Fern; see
  `tests/stdlib/`. `fern -cover` instruments a build for line and branch
  coverage and `fern -cover-report -lcov` writes an lcov tracefile.
- **Debugging.** `fern -sanitize` catches double frees and use-after-free and
  prints a leak census at exit; `-g` emits a symbol table; fatal aborts print
  a frame-pointer backtrace by default. `docs/SANITIZER.md`.
- **Literate programming.** A `.fern.md` file is a Markdown document whose
  named code chunks are tangled into a program and works anywhere a `.fern`
  file does; `fern -weave -html` turns it back into a cross-referenced page
  and `-doctest` runs its example blocks. `docs/LITERATE.md`.
- **Embedding and sharing.** `-embed DIR` compiles assets into the binary;
  `-shared -export` emits a `.so` loadable with `dlopen` or Android's
  `System.loadLibrary`. `docs/EMBED.md`.
- **Editor support.** `fern-lsp` plus a VS Code extension in `editors/`.
- **Async.** Colourless concurrency through `std/async` combinators over
  `Future[T]`, with real overlapping socket I/O on the native backends.
  `docs/ASYNC.md`.

## The compiler

The compiler is written in Fern and lives under `compiler/`. It compiles
itself to a byte-identical fixpoint (`make distcheck`) and builds every
program in `coreutils/`. `make bootstrap` builds it from a checkout with no
Go installed, using a pinned earlier release (`docs/BOOTSTRAP.md`), and
`make selfhost-cli` builds it with the `fern` you already have.

The Go code under `internal/` is the oracle, not a second compiler: a
parser, type checker and interpreter that the differential tests run every
program against, plus `-fmt`, the LSP and the package tools. It has had no
backends since 2026-10-05 (`docs/NATIVE-RETIREMENT.md`), and it learns a
language feature only after `compiler/` has it, so the differentials can
check it.

The `coreutils/` tree is GNU coreutils reimplemented in Fern, held to
byte-for-byte output parity with GNU and benchmarked against GNU and the
Rust uutils. It is the standing conformance and performance check: a
divergence has the same standing as a miscompile. `docs/COREUTILS.md`.

## How the compiler works

```
source → lexer → parser → type checker → monomorphisation → closure conversion
       → IR lowering → IR optimisation → refcount insertion and elision
       → backend (arm64 / x86-64 / wasm) → in-process assembler + linker
```

The IR is a stack-machine bytecode with structured control flow that every
backend consumes, so optimisation passes live in one place: inlining,
tail-call elimination, constant propagation and folding, strength
reduction, dead-code elimination, and fusion of `std/array` combinator
chains into single loops. Reference counting follows the Perceus approach:
increments and decrements are placed at compile time, borrowed parameters
skip them, and an allocation whose last reference is dropped can be reused
in place for a fresh one of the same shape.

```
compiler/             the compiler, written in Fern (fern.fern is its entry; drivers/ holds its test drivers)
cmd/fern/             CLI driver           cmd/fern-lsp/       language server
cmd/ferndoc/          stdlib doc generator cmd/fern-wasm/      playground bundle
internal/syntax/      the Go oracle's lexer, parser, AST, printer and diagnostics
internal/check/       its type checker and the rules it runs
internal/oracle/      its interpreter and REPL, and the passes from a checked program to them
internal/pkg/         module loading, manifests, the package store and capabilities
internal/tools/       the LSP, linter, literate tools and the launcher
internal/tables/      generated tables: assembler vocabularies, errno text, libm data
internal/testing/     the end-to-end suites (e2e, e2ecompiler) and test support
internal/wasm/        WebAssembly and component-model encoders
internal/stdlib/      the standard library, written in Fern
examples/             example programs
tests/                Fern-side tests: stdlib/ (std/test suites), probes/ (leak probes), proposals/ (defect repros)
conformance/          the conformance corpus
bench/                benchmark programs
coreutils/            GNU coreutils in Fern
bootstrap/            the pinned stage0 and `make bootstrap`
scripts/              repo tooling: CI gates, generators, benchmarks
spec/ docs/           the language spec and design notes
site/ web/ editors/   documentation site, playground, editor support
```

## Developing

Tool versions are pinned in `mise.toml`; `eval "$(scripts/toolchain-env)"`
installs them.

```sh
make build        # go build -> bin/fern
make test         # go test ./...
make examples     # compile every examples/*.fern
make bootstrap    # build the compiler without Go
```

The end-to-end suites in `internal/testing/e2e`, `internal/testing/e2ecompiler`, and the
differential tests run programs under qemu-aarch64, natively on x86-64 and
Apple Silicon, and under wasmtime. A missing runtime makes a test skip, and
a skip is a missing dependency rather than a pass. Which suite proves what,
and which ones look authoritative but are not, is in `docs/TEST-GATES.md`;
timings and memory budgets are in `docs/LOCAL-DEV-LOOP.md`. Design notes,
decisions, and plans live in `docs/`.
