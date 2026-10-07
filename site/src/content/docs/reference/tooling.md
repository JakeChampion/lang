---
title: Tooling
description: Compiler flags, targets, formatter, linter, testing and coverage, debugging, language server, editor extension.
sidebar:
  order: 6
---

## Compiler — `fern`

```bash
fern [-target TARGET] [-o OUTPUT] [-run] FILE.fern [-- ARGS...]
fern -check FILE.fern            # type-check only
fern -interp FILE.fern           # run through the interpreter, no binary
fern -fmt [-w | -d | -o OUT] FILE...
fern -lint PATH...
fern -repl
```

`fern` is one binary that formats, lints, type-checks, interprets,
compiles, and manages packages. A `-target` compile runs Fern's compiler,
which is itself written in Fern: it assembles, links, and (for Darwin)
code-signs in-process, so no external toolchain is needed. `fern` uses
`$FERN_SELFHOST` when it is set, else a `fern-selfhost` binary next to
itself, else it builds one on first use from the compiler sources it
embeds and caches the result. That first build downloads a pinned,
checksum-verified earlier release to do the compiling, so the very first
compile on a machine takes longer than the rest.

`-check`, `-interp`, `-repl`, `-fmt` and `-lint` do not need the
compiler and run straight away.

### Targets

| Target | Output |
| --- | --- |
| `arm64-linux` (default) | Static ELF. ARMv8.2-A with the crypto extensions. |
| `arm64-android` | Static position-independent ELF for Android. |
| `arm64-darwin` | Static, signed Mach-O for Apple Silicon (latest macOS). |
| `x86-64-linux` | Static ELF. x86-64-v3 (Haswell, 2013, with AVX2). |
| `wasm32-wasi` | WASI Preview 2 component; run with `wasmtime run`. |
| `wasm32-wasi-http` | `wasi:http/incoming-handler` component; run with `wasmtime serve`. |

Binaries are static and do no CPU feature detection at run time, so the
baseline is a requirement, not a fast path. `fern -targets`
lists every target with the capabilities its host provides — a program
that calls something its target lacks (`subprocess` on wasm, say) fails
to compile with E066 instead of failing at run time.

### Build flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-target` | `arm64-linux` | Target to compile for; see the table above. |
| `-o` | none | Output path. Without it a native target prints its assembly to stdout; a wasm target requires `-o`. |
| `-run` | off | Build to a temporary binary and run it, passing on its exit code. Runs directly when the target's ISA is the host's, otherwise under `-qemu`. Also spelled `--run`. |
| `-qemu` | `qemu-aarch64` | User-mode emulator for `-run` on a foreign ISA. For `-target x86-64-linux` the default becomes `qemu-x86_64`. |
| `-O` | off | Release build: drop every `assert()` after type-checking, without evaluating its condition. `-check` and `-interp` always keep asserts. |
| `-g` | off | Emit a symbol table and DWARF so debuggers, `nm`, backtraces and profilers can name functions and source lines. |
| `-backtrace` | on | Print a frame-pointer backtrace under a fatal abort's cause line. `-backtrace=false` leaves the walk out of the binary. |
| `-sanitize` | off | Debug build with the heap checks on; see [Debugging](#debugging). |
| `-cover` | off | Instrument for line and branch coverage; see [Coverage](#coverage). |
| `-embed DIR` | none | Compile the files under `DIR` into the binary; see [Assets](#embedding-assets). |
| `-shared` | off | Emit a shared object (`.so`) instead of an executable; with `-export f,g` naming the functions to export. |
| `-emit` | target's own | Wasm only: `core-module` for a raw core module, `command-module` for a WASI preview-1 command module. |
| `-backend` | target's own | Code generator. `ssa`, the register allocator, is the only one on the native ISAs. |
| `-color` | `auto` | Colour diagnostics: `auto`, `always`, or `never`. `NO_COLOR` turns `auto` off. |

### Program arguments

Driver flags go before `FILE`; everything after it goes to the program,
which reads it with `args()`. Put a `--` first when the program's own
arguments start with `-`:

```bash
fern -interp grep.fern -- -i pattern notes.txt
fern -target x86-64-linux -run grep.fern -- -i pattern notes.txt
```

Both engines see the same `args()[1:]`. A flag written after `FILE`
without the `--` is an error rather than being passed to the program
silently.

### Other modes

| Command | What it does |
| --- | --- |
| `fern -check FILE` | Type-check `FILE` and its imports. Silent on success; prints diagnostics and exits 1 on the first error. `-` reads stdin. Given a package directory it checks the package; given a workspace root it checks every member. |
| `fern -interp FILE` | Run through the interpreter: no codegen, no binary. `main()`'s return value is the exit code. `-` reads stdin. |
| `fern -repl` | Interactive session; see [REPL](#repl). |
| `fern -explain E030` | Print the long-form explanation of an error code. An unknown code prints the list of known ones. |
| `fern -targets` | List the targets and their capabilities. |
| `fern -capabilities FILE` | Report which of `net`, `fs`, `env`, `subprocess`, `time`, `random` each package can reach, with an example call chain. |
| `fern -effects FILE` | The same report per function instead of per package. |
| `fern -version` | Print the commit this binary was built from, plus the Go version and platform. |

The package commands (`-add`, `-fetch`, `-resolve`, `-vendor`) are on the
[Packages](../packages/) page. `fern -help` lists every flag.

## Formatter

`fern -fmt` rewrites a file to the canonical style: two-space
indent, one statement per line, trailing newline at EOF. Comments
survive the round trip in their original position.

```bash
fern -fmt foo.fern              # print the formatted file
fern -fmt -w foo.fern           # rewrite in place
fern -fmt -d foo.fern           # show the diff; exits 1 if the file would change
fern -fmt -o out.fern foo.fern  # write to another file
```

The formatter is idempotent: formatting its own output changes nothing.
On a literate `.fern.md` file it formats the code inside each chunk and
leaves the prose alone.

## Linter

`fern -lint` reports code that compiles but costs the next reader more
than it should. It works on the parse tree only, so a file with a type
error still lints, and a whole directory costs one parse per file.

```bash
fern -lint src/                 # every .fern file under src/
fern -lint-rules                # the rules, their default severity and options
fern -lint -lint-set cyclomatic-complexity=deny \
           -lint-opt cyclomatic-complexity.max=15 src/
```

A finding at `warn` prints and exits 0; one at `deny` exits 1. Set
severities and options for a whole package in its `fern.toml`:

```toml
[lint]
cyclomatic-complexity = "deny"

[lint.options]
cyclomatic-complexity.max = 15
```

`-lint-set` and `-lint-opt` on the command line win over the manifest. A
`// fern-lint: allow cyclomatic-complexity` comment on the line above a
function exempts that one function.

## Running tests

Fern's pure-Fern test runner lives in [`std/test`](../../stdlib/test/).
Tests are ordinary `.fern` files — run them with `-interp`, or compile
and run them:

```bash
fern -interp my_test.fern                        # interpreter
fern -target x86-64-linux -run my_test.fern      # compile + run
```

Output is [TAP-13](https://testanything.org/). Exit code is `0`
when every case passes and `1` on any failure, so any TAP-aware CI
runner (`prove`, `tape`, `tap-junit`) works without further config.

See the [Testing tutorial](../../tutorial/testing/) for the
authoring shape and assertion catalogue.

### Coverage

`-cover` builds a binary that counts every executed line and both arms
of every `if`, `while`, `&&` and `||`, then writes the counts to stderr
when it exits. `fern -cover-report` reads that output back:

```bash
fern -target x86-64-linux -cover -o my_test my_test.fern
./my_test 2> cover.txt
fern -cover-report cover.txt              # per-file line and branch totals, uncovered lines
fern -cover-report -lcov cover.txt > lcov.info
```

Lines without the `fern-cover:` prefix are ignored, so saving the
program's whole stderr is fine, and `-` reads stdin. Coverage is
available on the native x86-64 and arm64 targets; asking for it on wasm
is an error rather than an uninstrumented build.

## Debugging

A fatal abort — an out-of-range index, an exhausted arena — names its
cause on stderr and prints a frame-pointer backtrace under it. Build
with `-g` and the addresses resolve to function names through `nm` or
`addr2line`.

`-sanitize` turns on the heap checks together: it reports a double free
(a reference count released too many times) or a use-after-free as a
`fern-sanitizer:` line plus a backtrace, and prints a leak census when the
program exits. A clean run prints no `fern-sanitizer:` line, and its
output and exit code are unchanged.

```bash
fern -target x86-64-linux -sanitize -g -o prog prog.fern && ./prog
```

The sanitizer never reuses freed memory, so it is for debugging, not for
shipping. All three checks run on the native x86-64 and arm64 targets;
on wasm only the leak census is available.

## Embedding assets

`-embed DIR` compiles every file under `DIR` into the binary.
`__fern_asset("NAME")` is replaced at compile time by a string literal
holding the file's bytes, where `NAME` is the file's path relative to
`DIR` with `/` separators. `__fern_assets()` yields every asset as a
`(name, contents)` pair, sorted by name.

```fern
const PAGE: string = __fern_asset("html/index.html");

function main(): i32 {
    print(PAGE);
    for a in __fern_assets() {
        print(a.0);   // html/index.html, site.css, …
    }
    return 0;
}
```

```bash
fern -embed ./assets -target x86-64-linux -o site site.fern
```

Assets are ordinary string literals, so binary files work unchanged and
reading one costs nothing at run time.

## Shared libraries

`-shared` emits a position-independent `.so` whose dynamic symbol table
exports the functions named by `-export` (default `main`). It loads with
`dlopen`, or `System.loadLibrary` on Android:

```fern
pub function add(a: i32, b: i32): i32 {
    return a + b;
}
```

```bash
fern -target x86-64-linux -shared -export add -o libadd.so add.fern
```

`-shared` needs `-o` and an ELF target: `x86-64-linux`, `arm64-linux` or
`arm64-android`.

## Language server — `fern-lsp`

Speaks LSP over stdin/stdout. Spawn it from any editor with a
generic LSP client. Install it with Go:

```bash
go install github.com/jakechampion/lang/cmd/fern-lsp@latest
```

Features:

- **Diagnostics** — parser + type-check errors, routed per-file in
  multi-module programs.
- **Hover** — types for variables, parameters, fields, methods,
  cross-module references.
- **Goto-definition** — across files in workspace mode.
- **Completion** — locals, params, top-level decls, variants,
  keywords. Triggered on `.` and Ctrl/Cmd-Space.
- **Signature help** — function signatures with active-parameter
  highlighting.
- **Inlay hints** — inferred types for `let x = …`.
- **Document symbols** — outline view (Cmd-Shift-O in VS Code).
- **Semantic tokens** — type-aware syntax highlighting.
- **Find references + rename** — workspace-wide, including method
  calls / struct fields / enum variants.
- **Quick fixes** — a diagnostic that carries a suggested edit offers
  it as a code action.
- **Format on save** — runs the formatter via
  `textDocument/formatting`.

Literate `.fern.md` documents get diagnostics on the document's own
lines, plus hover, goto-definition, references, completion and signature
help inside their code chunks.

## VS Code extension

Lives at `editors/vscode/`. Install with:

```bash
cd editors/vscode
npm install
npm run compile
npx @vscode/vsce package
code --install-extension fern-vscode-0.1.0.vsix
```

| Setting | Default | Meaning |
| --- | --- | --- |
| `fern.serverPath` | `fern-lsp` | Path to the server binary; looked up on `$PATH` when unqualified. |
| `fern.serverArgs` | `[]` | Arguments passed to the server. |
| `fern.trace.server` | `off` | Log LSP traffic: `messages` or `verbose`. |

The **Fern: Restart Server** command restarts `fern-lsp`, which is useful
after rebuilding it.

## REPL

```bash
$ fern -repl
> let x = 7;
> x * 2
14
> function sq(n: i32): i32 { return n * n; }
> sq(x)
49
```

State persists across lines. Each line is one input: an expression
prints its value, a statement runs silently, and a function declaration
is registered for later lines. A function has to fit on one line.

## Literate programming

Fern supports Knuth-style literate programming: a `.fern.md` document
interleaves prose and code in named chunks, and the toolchain extracts
(*tangles*) the compilable source or renders (*weaves*) a cross-referenced
document.

```bash
fern -tangle -o prog.fern prog.fern.md             # extract Fern source
fern -tangle -chunk 'the main loop' prog.fern.md   # print one chunk
fern -weave -html -o prog.html prog.fern.md        # render the document
fern -doctest prog.fern.md                         # run its ```fern test examples
```

Flags go before the document, as with every other mode.

`-check`, `-interp` and `-fmt` take a `.fern.md` directly: they tangle
it in memory, and diagnostics point at the document's own lines. Under
those modes a single-root `.fern.md` can also be `import`ed as a library
from ordinary `.fern` code. A `-target` compile does not tangle yet
([#11838](https://github.com/JakeChampion/lang/issues/11838)), so to
build a binary, tangle first:

```bash
fern -tangle -o prog.fern prog.fern.md
fern -target x86-64-linux -o prog prog.fern
```

A ```` ```fern test ```` block is a runnable example: `fern -doctest`
tangles each one into its own program, compiles and runs it, and reports
TAP. An example passes when `main` returns 0.

The chunk grammar, multi-file `file=` directives, and the provenance
model are documented in [`docs/LITERATE.md`][lit]; runnable examples live
under [`examples/literate/`][ex].

[lit]: https://github.com/JakeChampion/lang/blob/main/docs/LITERATE.md
[ex]: https://github.com/JakeChampion/lang/tree/main/examples/literate
