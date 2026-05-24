---
title: Tooling
description: Compiler flags, formatter, language server, editor extensions.
sidebar:
  order: 3
---

## Compiler — `fern`

```bash
fern [-target arm64|arm64-darwin|x86-64|wasm] [-o OUTPUT] [--run] FILE.fern
fern -fmt [-w | -d] FILE.fern
fern -interp FILE.fern
fern -repl
```

### Common flags

| Flag       | Default | Meaning                                                    |
| ---------- | ------- | ---------------------------------------------------------- |
| `-target`  | `arm64` | Backend: arm64, arm64-darwin, x86-64, wasm.                |
| `-o`       | stdout  | Output binary path. Without it, assembly prints to stdout. |
| `--run`    | off     | Compile + execute the produced binary. Returns its exit code. |
| `-cc`      | `cc`    | Linker for native targets (`gcc`, `clang`, ...).           |
| `-qemu`    | none    | Path to a qemu-* binary for cross-arch execution under `--run`. |
| `-fmt`     | off     | Format the source. `-w` writes back; `-d` prints a diff.   |
| `-interp`  | off     | Run the AST interpreter (skips codegen entirely).          |

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

State persists across lines. Multi-line forms (an `if` block,
function decl, etc.) are one logical input — the REPL reads until
braces balance.

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
