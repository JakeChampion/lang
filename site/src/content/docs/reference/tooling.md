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
fern -fmt [-w | -d | -o OUT] PATH...
fern -lint PATH...
fern -repl
```

`fern` is one binary that formats, lints, type-checks, interprets,
compiles, and manages packages. It is Fern's compiler, written in Fern, with
the standard library inside it: it assembles, links, and (for Darwin)
code-signs in-process, so no external toolchain is needed.

These pages write every flag with one dash, and that is the canonical
spelling. `fern` also accepts two dashes, takes a value as `-target
x86-64-linux` or `-target=x86-64-linux`, and takes `=true` or `=false` on
any flag that has no value: `-cover=false`, `-g=true`.

### Targets

| Target | Output |
| --- | --- |
| `arm64-linux` (default) | Static ELF. ARMv8.2-A with the crypto extensions. |
| `arm64-android` | Static position-independent ELF for Android. |
| `arm64-darwin` | Static, signed Mach-O for Apple Silicon (latest macOS). |
| `x86-64-linux` | Static ELF. x86-64-v3 (Haswell, 2013, with AVX2). |
| `wasm32-wasi` | WASI Preview 2 component; run with `wasmtime run`. |
| `wasm32-wasi-http` | `wasi:http/incoming-handler` component; run with `wasmtime serve`. |
| `arm64-freestanding`, `x86-64-freestanding` | Check only (`fern -check -target …`): no OS underneath, so no host capabilities at all. No backend emits for them yet. |

Binaries are static and do no CPU feature detection at run time, so the
baseline is a requirement, not a fast path. `fern -targets`
lists every target with the capabilities its host provides — a program
that calls something its target lacks (`subprocess` on wasm, say) fails
to compile with E066 instead of failing at run time.

### Build flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `-target` | `arm64-linux` | Target to compile for; see the table above. |
| `-o` | none | Output path. Without it a native target prints its assembly to stdout; a wasm target requires `-o` unless `-emit asm` asks for its WAT. |
| `-run` | off | Build to a temporary binary and run it, passing on its exit code. Runs directly when the target's ISA is the host's, under `wasmtime run` for `wasm32-wasi` (a component reports only 0 or 1; `-emit command-module` keeps the full code), otherwise under `-qemu`. |
| `-qemu` | `qemu-aarch64` | User-mode emulator for `-run` on a foreign ISA. For `-target x86-64-linux` the default becomes `qemu-x86_64`. |
| `-O` | off | Release build: drop every `assert()` after type-checking, without evaluating its condition. `-check` and `-interp` always keep asserts. |
| `-g` | off | Emit a symbol table and DWARF so debuggers, `nm`, backtraces and profilers can name functions and source lines. |
| `-size` | off | Build for size over speed, and leave the backtrace walk out unless `-backtrace` asks for it. `FERN_SIZE=1` makes it the default. |
| `-harden` | off | `arm64-linux` executables only: BTI landing pads at every indirect-call target, and return addresses signed with pointer authentication. |
| `-backtrace` | on | Print a frame-pointer backtrace under a fatal abort's cause line. `-backtrace=false` leaves the walk out of the binary. |
| `-sanitize` | off | Debug build with the heap checks on; see [Debugging](#debugging). |
| `-cover` | off | Instrument for line and branch coverage; see [Coverage](#coverage). |
| `-embed DIR` | none | Compile the files under `DIR` into the binary; see [Assets](#embedding-assets). |
| `-shared` | off | Emit a shared object (`.so`) instead of an executable; with `-export f,g` naming the functions to export. |
| `-emit` | target's own | `asm` stops at the generated assembly (GAS on the native targets, WAT on wasm). On wasm, `core-module` gives a raw core module and `command-module` a WASI preview-1 command module. |
| `-backend` | target's own | Code generator. `ssa`, the register allocator, is the only one on the native ISAs. |
| `-color` | `auto` | Colour diagnostics: `auto`, `always`, or `never`. `NO_COLOR` turns `auto` off. |
| `-ascii` | auto | Draw diagnostics with ASCII only. Chosen automatically when the locale is not UTF-8. |
| `-q` | off | Print one line per diagnostic, without source context, notes, fixes or colour. |

### Environment variables

Every on/off `FERN_*` variable reads its value the same way: unset, empty,
`0`, `false` and `off` (in any case) are off, and anything else is on. A
variable that also names a mode, such as `FERN_PROFILE`, is off for the same
values. A directory variable set to the empty string counts as unset. The
compiler variables are read when compiling, so they go on the build, not on
the run.

| Variable | Meaning |
| --- | --- |
| `FERN_SIZE` | The default for `-size`. |
| `FERN_BACKTRACE` | The default for `-backtrace`; on while unset, so `FERN_BACKTRACE=0` leaves the walk out. |
| `FERN_SANITIZE` | The same as `-sanitize`. |
| `FERN_LEAKCHECK` | The leak census of `-sanitize` on its own. |
| `FERN_COVER` | The same as `-cover`. |
| `FERN_ARRAY_REPORT` | Print the `-array-report` report on stderr during an ordinary compile. |
| `FERN_PROFILE` | `cpu`, `alloc`, `cpu-pprof` or `alloc-pprof`: build a self-profiling executable. `FERN_PROFILE_FILE` names the pprof output. |
| `FERN_DIAG_FORMAT` | `terse` is the same as `-q`. |
| `FERN_CACHE_DIR` | Where `fern` keeps its cache, in place of the user cache directory. |
| `FERN_KTLS`, `FERN_IO_URING` | Read at run time by a program using `std/fetch` or `std/serve`; see [protocols](../../http/protocols/). |

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
| `fern -explain E030` | Print the long-form explanation of an error code. A bare `fern -explain` lists the codes, and an unknown code prints the list too. |
| `fern -targets` | List the targets and their capabilities. |
| `fern -capabilities FILE` | Report which of `net`, `fs`, `env`, `subprocess`, `time`, `random` each package can reach, with an example call chain. |
| `fern -effects FILE` | The same report per function instead of per package. |
| `fern -array-report FILE` | Compile `FILE` for the target and print the array decisions the compiler made: each `std/array` chain's fusion verdict and the map storage and ndarray kernel decisions, each with the reason that decided it. Writes no output, so it takes neither `-o` nor `-run`. |
| `fern -doc FILE` | A Markdown reference page from a module's doc comments; `-o OUTDIR DIR` writes one page per module. |
| `fern -version` | Print the commit this binary was built from and the target it was built for. |

The package commands (`-add`, `-fetch`, `-resolve`, `-vendor`) are on the
[Packages](../packages/) page. `fern` with no arguments prints a usage
summary.

## Formatter

`fern -fmt` rewrites a file to the canonical style: two-space
indent, one statement per line, trailing newline at EOF. Comments
survive the round trip in their original position.

```bash
fern -fmt foo.fern              # print the formatted file
fern -fmt -w foo.fern           # rewrite in place
fern -fmt -d foo.fern           # show the diff; exits 1 if the file would change
fern -fmt -o out.fern foo.fern  # write to another file
fern -fmt -w src/               # every .fern and .fern.md file under src/
fern -fmt - < foo.fern          # stdin to stdout, as an editor pipes a buffer
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
fern -lint - < foo.fern         # a buffer on stdin
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

`-embed DIR` compiles every file under `DIR` into the binary. `NAME` is
a file's path relative to `DIR` with `/` separators. Like `read_file` and
`read_file_bytes`, each asset builtin has a text form and a bytes form:

| Builtin | Yields |
| --- | --- |
| `__fern_asset("NAME")` | the file as a `string`; a file that is not valid UTF-8 is a compile error |
| `__fern_asset_bytes("NAME")` | the file as a `u8[]` |
| `__fern_assets()` | every file as a `(name, text)` pair, sorted by name; any file that is not valid UTF-8 is a compile error |
| `__fern_assets_bytes()` | every file as a `(name, bytes)` pair, sorted by name |

```fern
const PAGE: string = __fern_asset("html/index.html");
const ICON: u8[] = __fern_asset_bytes("img/favicon.png");

function main(): i32 {
    print(PAGE);
    for a in __fern_assets_bytes() {
        print(a.0);   // html/index.html, img/favicon.png, …
    }
    return 0;
}
```

```bash
fern -embed ./assets -target x86-64-linux -o site site.fern
```

Each call becomes an ordinary literal, so reading an asset costs nothing
at run time.

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

## Language server — `fern -lsp`

Speaks LSP over stdin/stdout. Spawn `fern -lsp` from any editor with a
generic LSP client; it is part of the `fern` binary, so there is nothing
else to install. `-target T` checks documents against that target's
capabilities, as `fern -check -target T` does.

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
| `fern.serverPath` | `fern` | Path to the `fern` binary; looked up on `$PATH` when unqualified. |
| `fern.serverArgs` | `["-lsp"]` | Arguments passed to the server. |
| `fern.trace.server` | `off` | Log LSP traffic: `messages` or `verbose`. |

The **Fern: Restart Server** command restarts the server, which is useful
after rebuilding `fern`.

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

A `.fern.md` works anywhere a `.fern` file does. `fern -target
x86-64-linux -o prog prog.fern.md`, `-run`, `-check`, `-interp` and
`-fmt` all take one directly: the document is tangled in memory, and
diagnostics point at the document's own lines. A single-root `.fern.md`
can also be `import`ed as a library from ordinary `.fern` code, and a
document of `file=` blocks compiles as the modules it tangles to.

A ```` ```fern test ```` block is a runnable example: `fern -doctest`
tangles each one into its own program, compiles and runs it, and reports
TAP. An example passes when `main` returns 0.

The chunk grammar, multi-file `file=` directives, and the provenance
model are documented in [`docs/LITERATE.md`][lit]; runnable examples live
under [`examples/literate/`][ex].

[lit]: https://github.com/JakeChampion/lang/blob/main/docs/LITERATE.md
[ex]: https://github.com/JakeChampion/lang/tree/main/examples/literate
