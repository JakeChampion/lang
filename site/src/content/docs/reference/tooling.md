---
title: Tooling
description: Compiler flags, formatter, language server, editor extensions.
sidebar:
  order: 8
---

## Compiler — `fern`

`fern` has no subcommands. Every mode is a top-level flag on the one
binary:

```bash
fern [-target <target>] [-o OUTPUT] [--run] FILE.fern
fern -fmt [-w | -d] FILE.fern
fern -check FILE.fern | DIR
fern -interp FILE.fern
fern -repl
```

The list below is the part you reach for daily. `fern -h` prints a shorter
banner than the binary actually supports, so prefer this page — or the
source — when looking for a mode you half-remember.

### Building

| Flag | Default | Meaning |
| ---- | ------- | ------- |
| `-target` | `arm64-linux` | Which target to emit for. See [Targets and backends](../../compiler/targets/); `fern -targets` prints the live table. |
| `-o` | stdout | Output path. Without it, assembly prints to stdout. |
| `--run` | off | Compile, then execute the produced binary and return its exit code. |
| `-O` | off | Release build: drop every `assert()` after type-checking. |
| `-g` | off | Emit a symbol table so debuggers, `nm` and profilers can name functions. |
| `-backtrace` | on | Frame-pointer backtrace on a fatal abort. `-backtrace=false` strips the walker and its strings. |
| `-shared` / `-export` | off | Emit a position-independent `.so` with a dynamic symbol table. Native ELF targets only. |
| `-embed DIR` | none | Bake a directory of assets in at compile time; reach them with `__fern_asset("name")`. |
| `-backend` | default | `ssa` selects the register-allocating emitter. |
| `-emit` | component | `core-module` or `command-module` change what the wasm backend wraps its output in. |
| `-cc` | none | Assemble and link through an external toolchain instead of the built-in one. |
| `-qemu` | none | Path to a `qemu-*` binary for cross-architecture execution under `--run`. |

Native targets assemble *and* link without an external toolchain, so
`-cc` is an opt-out rather than a requirement.

### Checking, running, explaining

| Flag | Meaning |
| ---- | ------- |
| `-check [FILE\|DIR]` | Type-check and exit. Silent on success. A directory checks a package's entry module, or every member of a workspace. |
| `-interp [FILE]` | Run the AST interpreter, skipping codegen. `main`'s return becomes the exit code. |
| `-repl` | Interactive prompt. |
| `-fmt` | Format the source. `-w` writes back; `-d` prints a diff and exits 1 if anything differed. |
| `-explain E048` | Print the long-form explanation of a diagnostic code. |
| `-targets` | Print every target with its capability surface. |
| `-version` | Print the commit this binary was built from, plus the Go version and platform. |
| `-color`, `-ascii` | Diagnostic rendering. |

### Measuring and auditing

| Flag | Meaning |
| ---- | ------- |
| `-lint PATH…` | Run the [linter](#linter). `-lint-rules` lists rules; `-lint-set R=SEV` and `-lint-opt R.OPT=V` override config. |
| `-cover` / `-cover-report` | Line and branch coverage. The report mode prints a summary, or an lcov tracefile with `-lcov`. |
| `-sanitize` | Heap checker: double free, use-after-free, and a leak census at exit. Native targets only. |
| `-append-report FILE` | Every `.append` site and whether it grew in place or copied. |
| `-capabilities FILE` | What each package can reach, with an example call chain. |
| `-effects FILE` | The effect row of each function. A report, not an enforcement. |

### Packages and documents

| Flag | Meaning |
| ---- | ------- |
| `-add NAME SPEC` | Add a dependency to `fern.toml`, editing it textually so comments survive. |
| `-fetch` | Download and verify dependency archives. The only command that touches the network. |
| `-resolve` | Minimum version selection over the index; writes `fern.lock`. |
| `-vendor` | Flatten the dependency graph into `vendor/` so later builds are fully offline. |
| `-tangle`, `-weave`, `-html`, `-chunk`, `-doctest` | The [literate](#literate-programming) toolchain. |

## Formatter

`fern -fmt` rewrites a file to the canonical style: two-space
indent, one statement per line, trailing newline at EOF. Comments
survive the round trip in their original position.

```bash
fern -fmt -w foo.fern   # rewrite in place
fern -fmt -d foo.fern   # show the diff instead
```

The formatter is idempotent: `fern -fmt | fern -fmt` produces
identical output.

## Language server — `fern-lsp`

Speaks LSP over stdin/stdout. Spawn it from any editor with a
generic LSP client. Features:

- **Diagnostics** — parser + type-check errors, routed per-file in
  multi-module programs.
- **Hover** — types for variables, parameters, fields, methods,
  cross-module references.
- **Goto-definition** — across files in workspace mode.
- **Completion** — locals, params, top-level decls, variants,
  keywords. Triggered on `.` and Ctrl/Cmd-Space.
- **Signature help** — function signatures with active-parameter
  highlighting.
- **Inlay hints** — inferred types for `var x = …`.
- **Document symbols** — outline view (Cmd-Shift-O in VS Code).
- **Semantic tokens** — type-aware syntax highlighting.
- **Find references + rename** — workspace-wide, including method
  calls / struct fields / enum variants.
- **Format on save** — runs the formatter via
  `textDocument/formatting`.

## VS Code extension

Lives at `editors/vscode/`. Install with:

```bash
cd editors/vscode
npm install
npm run compile
npx @vscode/vsce package
code --install-extension fern-vscode-0.1.0.vsix
```

Set `fern.serverPath` if `fern-lsp` isn't on your `$PATH`.

## REPL

```bash
fern -repl
> var x = 7;
> x * 2
14
```

State persists across lines, and each line is evaluated on its own — the
REPL does not read a multi-line form. A function declaration or an `if`
block has to fit on one line, or go in a file.

## Running tests

Fern's pure-Fern test runner lives in [`std/test`](../../stdlib/test/).
Tests are ordinary `.fern` files — run them with `-interp` (or
compile + execute the produced binary):

```bash
fern -interp my_test.fern        # AST interpreter
fern my_test.fern -o my_test --run   # compile + run
```

Output is [TAP-13](https://testanything.org/). Exit code is `0`
when every case passes and `1` on any failure, so any TAP-aware CI
runner (`prove`, `tape`, `tap-junit`) works without further config.

See the [Testing tutorial](../../tutorial/testing/) for the
authoring shape and assertion catalogue.

## Linter

`fern -lint` runs on the **parse tree** — no type-checking, no import
resolution. Three things follow: a file with a type error still lints, a
whole tree costs one parse per file, and a rule that needs types belongs in
the checker instead.

Two rules ship today:

| Rule | Default | Options |
| ---- | ------- | ------- |
| `cyclomatic-complexity` | `warn` | `max` (default 10) |
| `ambient-capability` | `warn` | — |

Severity is `allow` (the rule does not run), `warn` (prints, exit 0) or
`deny` (prints, exit 1). Innermost wins: the rule's default, then
`fern.toml`, then `-lint-set` on the command line.

```toml
[lint]
cyclomatic-complexity = "deny"

[lint.options]
cyclomatic-complexity.max = 25
```

Suppress a rule with a comment. Alone on a line it covers the next
code-bearing line; trailing a line it covers that line; `allow-file`
covers the whole file.

```fern
// fern-lint: allow cyclomatic-complexity
// dispatch_op is a flat table; splitting it would only hide the shape.
function dispatch_op(op: i32): i32 { return op; }
```

A directive naming a rule that does not exist is itself reported, so a
typo fails loudly rather than silently disabling nothing.

## Coverage and the sanitizer

```bash
fern -cover -o app app.fern && ./app     # writes coverage data
fern -cover-report app.fern              # human summary
fern -cover-report -lcov app.fern        # lcov tracefile for CI
```

Coverage is line **and** branch, and is available on the native targets
only — a wasm build under `-cover` errors rather than quietly producing an
uninstrumented binary.

`fern -sanitize` builds with a heap checker: reference-count over-release
(double free), use-after-free of a quarantined block, and a leak census
printed at exit. Also native-only, and it says so loudly when the target is
not covered rather than reporting a clean run it did not perform.

## Literate programming

Fern supports Knuth-style literate programming: a `.fern.md` document
interleaves prose and code in named chunks, and the toolchain extracts
(*tangles*) the compilable source or renders (*weaves*) a cross-referenced
document.

```bash
fern -tangle prog.fern.md -o prog.fern   # extract Fern source
fern -weave  prog.fern.md -o prog.html -html   # render the document
```

A `.fern.md` entry compiles directly — `fern prog.fern.md` tangles it in
memory and runs the normal pipeline, with diagnostics mapped back onto
the document's source lines. A single-root `.fern.md` can also be
`import`ed as a library from ordinary `.fern` code.

The chunk grammar, multi-file `file=` directives, and the provenance
model are documented in [`docs/LITERATE.md`][lit]; runnable examples live
under [`examples/literate/`][ex].

[lit]: https://github.com/JakeChampion/lang/blob/main/docs/LITERATE.md
[ex]: https://github.com/JakeChampion/lang/tree/main/examples/literate
