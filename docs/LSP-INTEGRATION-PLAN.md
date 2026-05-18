# LSP integration plan

## Goal

Ship a Language Server Protocol implementation for `lang` and surface it in
the existing web playground (`web/index.html`) so users get diagnostics,
hover-for-type, go-to-definition, and completion both in their editor of
choice and in the browser.

## Why this is tractable

The compiler already exposes most of what an LSP needs as ordinary Go API:

- `parser.Parse(src) (*ast.Program, error)` collects **all** parser errors
  (returning `diag.Errors`), so we can surface every diagnostic in a single
  pass instead of stopping at the first.
- `checker.Check(prog) (*Info, error)` does the same for type errors, and
  populates an `Info` side table with `VarTypes`, `FuncSigs`, `Methods`,
  `Locals`, `Structs`, `Enums`. That's a ready-made symbol table.
- `internal/diag` defines structured error interfaces (`Positioned`,
  `Spanned`, `Hinted`) that map 1:1 onto LSP `Diagnostic` fields.
- `cmd/lang-wasm` already builds the compiler to `GOOS=js GOARCH=wasm` and
  exposes a JS-callable entry point. Adding a second entry point for LSP
  requests is mechanical.

## Significant gaps to close

1. **AST nodes only carry start positions.** `ast.Position{Line, Col}` is
   1-based and there's no companion `End`. Hover, semantic tokens, and
   go-to-def target ranges all need end positions.
2. **No AST visitor.** The checker uses ad-hoc type switches. LSP needs a
   single `ast.Walk(node, func)` to answer "which node is under the cursor".
3. **No incremental compilation.** Every edit re-parses + re-checks the
   whole file. Fine for playground-sized snippets; debounce for editors.
4. **Formatter strips comments.** Don't wire `textDocument/formatting` to
   `printer.Format` until comments survive a round trip.

## Architecture

### LSP server — `cmd/lang-lsp/main.go` (new)

A thin Go binary that:

1. Reads JSON-RPC over stdin/stdout per the LSP spec.
2. Maintains `map[uri]string` of open documents.
3. On `didOpen` / `didChange`: re-parses + re-checks, translates
   `diag.Errors` into `PublishDiagnosticsParams`, sends the notification.
4. On `textDocument/hover`: walks the AST, finds the node at the position,
   returns the type from `Info.VarTypes` (or the AST-attached type field).
5. On `textDocument/definition`: looks up identifiers in `Info` tables.
6. On `textDocument/completion`: enumerates `Info.FuncSigs`, locals from
   `Info.Locals`, struct/enum names, plus keywords.

Hand-roll the small subset of the LSP wire format we need (≈10 message
types for an MVP) rather than depend on `go.lsp.dev/protocol`. Cuts a
dependency and keeps the wasm build small.

### Playground wiring

Same wasm binary, in-process LSP. Extend `cmd/lang-wasm/main.go` to export
a second global `langLsp(jsonString) -> jsonString`. The browser drives the
editor, posts JSON-RPC messages synchronously into wasm, and gets
diagnostics / hover back. No worker, no server, no protocol mismatch.

Replace the `<textarea>` in `web/index.html` with **CodeMirror 6**
(~150 KB vs Monaco's ~2 MB; lint, hover, autocomplete extensions ship
separately so we only pay for what we use). Keep the existing `Run`
button and `langInterpret` flow untouched.

## PR ordering

Each step is independently shippable and independently testable.

1. **AST end-positions + `ast.Walk` helper.** Pure refactor, no behaviour
   change. Threads end positions through `parser` for the node kinds LSP
   touches first (Ident, Call, struct lit, var/func decls). Adds an
   `ast.Walk` visitor. Parser tests assert end positions; a new
   `ast/walk_test.go` covers traversal.

2. **`cmd/lang-lsp` MVP.** Implements `initialize`, `initialized`,
   `shutdown`, `exit`, `textDocument/didOpen`, `didChange`, `didClose`,
   `publishDiagnostics`. Golden JSON-RPC fixtures exercise a clean file
   and a file with parser + checker errors.

3. **`hover` + `definition`.** Depends on the AST walker from step 1.

4. **Playground hookup.** Swap textarea → CodeMirror 6 (loaded from
   esm.sh so we don't take a build-tooling dependency), add the
   `langLsp` wasm export, wire the lint + hover extensions. Keep
   `langInterpret` and the `Run` button as-is.

5. **`completion` + `signatureHelp`.** Mine `Info.FuncSigs` and
   `Info.Locals` for candidates, plus a hard-coded keyword list.

## Testing

- Each new package gets unit tests in the same PR.
- The LSP MVP gets end-to-end fixtures: feed a recorded JSON-RPC
  transcript, assert the response transcript matches. Keeps coverage
  hermetic and fast.
- Playground changes are smoke-tested in a headless browser
  (manual for the first PR; a Playwright harness is overkill until
  we have more than one interactive feature).

## Non-goals (for now)

- Incremental re-checking on edit.
- Formatting through the LSP (blocked on comment-preserving formatter).
- Workspace-wide features (find references across files, rename) — the
  module loader is filesystem-based and the playground is single-file;
  defer until both stories are clearer.
- A VS Code extension. The LSP binary is enough; users wire it up via
  their editor's generic LSP client.
