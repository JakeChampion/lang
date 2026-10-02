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
  var w: i32 = a.parse_int()?;
  var h: i32 = b.parse_int()?;
  return Some(Rect(w, h).area());
}

function main(): i32 {
  var shapes: Shape[] = [Circle(2), Rect(3, 4)];
  var total: i32 = 0;
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

Every native target is assembled and linked in-process, and the Darwin
binary is ad-hoc code-signed in-process too. Pass `-cc` to opt out to an
external assembler and linker. `fern -targets` lists every target with the
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
  `examples/tests/`. `fern -cover` instruments a build for line and branch
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

## The self-hosted compiler

Fern has two compilers. The Fern one under `examples/self_host/` is where
the language now lands: it compiles itself to a byte-identical fixpoint
(`make distcheck`) and builds every program in `coreutils/`. `make bootstrap`
builds it from a checkout with no Go installed, using a pinned earlier
release (`docs/BOOTSTRAP.md`), and `make selfhost-cli` builds it with the Go
compiler for the host you are on.

The Go compiler under `internal/` has been frozen since 2026-09-28. It is the
stage-0 bootstrap and the differential oracle, and accepts only bugfixes,
oracle needs, and what the self-host sources need to build
(`docs/NATIVE-FREEZE.md`, `docs/NATIVE-CONVERGENCE.md`). Retiring its
backends is the next roadmap step.

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
cmd/fern/             CLI driver           cmd/fern-lsp/       language server
cmd/ferndoc/          stdlib doc generator cmd/fern-wasm/      playground bundle
internal/lexer,parser,checker,monomorph,closureconv,ir   front end and IR
internal/codegen/     arm64/, x86_64/, wasmbin/ emitters
internal/native/      pure-Go assemblers, ELF and Mach-O linkers, code signing
internal/stdlib/std/  the standard library, written in Fern
internal/interp/      tree-walking interpreter and REPL
internal/e2e*/        end-to-end suites for every backend and the self-host
examples/self_host/   the compiler written in Fern
coreutils/            GNU coreutils in Fern
site/                 the documentation site
```

## Developing

Tool versions are pinned in `mise.toml`; `eval "$(scripts/toolchain-env)"`
installs them.

```sh
make build        # go build -> bin/fern
make test         # go test ./...
make examples     # compile every examples/*.fern
make bootstrap    # build the self-host compiler without Go
```

The end-to-end suites in `internal/e2e`, `internal/e2eselfhost`, and the
differential tests run programs under qemu-aarch64, natively on x86-64 and
Apple Silicon, and under wasmtime. A missing runtime makes a test skip, and
a skip is a missing dependency rather than a pass. Which suite proves what,
and which ones look authoritative but are not, is in `docs/TEST-GATES.md`;
timings and memory budgets are in `docs/LOCAL-DEV-LOOP.md`. Design notes,
decisions, and plans live in `docs/`.
