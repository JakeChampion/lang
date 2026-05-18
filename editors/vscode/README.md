# lang VS Code extension

Language Server Protocol + TextMate-grammar syntax highlighting for
the lang language. Provides:

- Inline error squiggles (parser + checker diagnostics).
- Hover-for-type on variables, parameters, fields, method calls,
  cross-module references.
- Go-to-definition (jumps across files in workspace mode).
- Completion + signature help.
- Inlay hints for unannotated `var x = …` declarations.
- Document symbols ("Outline" view + Cmd-Shift-O).
- Semantic tokens (type-aware highlighting on top of the
  TextMate-grammar fallback).
- Find references + rename across the whole workspace.
- Format on save (preserves comments).

## Install

1. Build `lang-lsp` and put it on your `$PATH`:

   ```bash
   go build -o ~/.local/bin/lang-lsp ./cmd/lang-lsp
   ```

   Or set an explicit path via the `lang.serverPath` setting.

2. Build + install the extension:

   ```bash
   cd editors/vscode
   npm install
   npm run compile
   # Then package + install, or symlink ~/.vscode/extensions/lang-vscode
   npx @vscode/vsce package
   code --install-extension lang-vscode-0.1.0.vsix
   ```

## Settings

| Setting             | Default     | Meaning                                     |
| ------------------- | ----------- | ------------------------------------------- |
| `lang.serverPath`   | `lang-lsp`  | Path to the binary. Absolute or on PATH.    |
| `lang.trace.server` | `off`       | LSP trace level. `messages` or `verbose`.   |

## Commands

- **lang: Restart Server** — kills + respawns lang-lsp. Useful
  when the binary is rebuilt while the extension is running.

## Troubleshooting

- "lang-lsp binary not found" — set `lang.serverPath` or add it
  to PATH.
- No diagnostics — turn on `lang.trace.server` to `verbose` and
  check the lang language server output channel for protocol
  errors.
- Slow / unresponsive on large files — the server doesn't yet do
  incremental re-checking; debounce defaults are tuned for files
  under a few KB.
