---
title: Tooling
description: Compiler flags, formatter, language server, editor extensions.
sidebar:
  order: 3
---

## Compiler — `lang`

```bash
lang [-target arm64|arm64-darwin|x86-64|wasm] [-o OUTPUT] [--run] FILE.lang
lang -fmt [-w | -d] FILE.lang
lang -interp FILE.lang
lang -repl
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

`lang -fmt` rewrites a file to the canonical style: two-space
indent, one statement per line, trailing newline at EOF.
Comments survive the round trip in their original position.

```bash
lang -fmt -w foo.lang   # rewrite in place
lang -fmt -d foo.lang   # show the diff instead
```

The formatter is idempotent: `lang -fmt | lang -fmt` produces
identical output.

## Language server — `lang-lsp`

Speaks LSP over stdin/stdout. Spawn it from any editor with a
generic LSP client.

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
code --install-extension lang-vscode-0.1.0.vsix
```

Set `lang.serverPath` if `lang-lsp` isn't on your `$PATH`.

## REPL

```bash
lang -repl
> var x = 7;
> x * 2
14
```

State persists across lines. Multi-line forms (an `if` block,
function decl, etc.) are entered as one logical input — the REPL
reads until braces balance.
