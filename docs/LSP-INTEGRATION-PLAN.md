# LSP integration plan

> **Status: MVP shipped** — `cmd/fern-lsp` exists (diagnostics, hover,
> go-to-def, completion) and the playground wires it via `cmd/fern-wasm`.
> Post-MVP performance work is surveyed in `IDE-COMPILATION-RESEARCH.md`
> and tracked in [#4415](https://github.com/JakeChampion/lang/issues/4415).
> The self-host compiler serves diagnostics and formatting as `fern -lsp`
> (#6641) — see the next section; everything below it describes the Go
> server.

## The self-host server: `fern -lsp`

`fern -lsp [-target T] [-embed DIR] [stdlib-root]` runs the self-host CLI as a
language server on stdin and stdout. It is #6641's first slice: diagnostics and
formatting, and none of the cursor features yet.

- **The wire** is `compiler/lsp.fern`. It does Content-Length framing
  over `stdin()`, JSON-RPC through `std/json`, the UTF-16 position conversion
  (#8468) and file:// URIs to paths. It knows nothing of the compiler.
- **Diagnostics are `-check`'s findings.** `fern.fern`'s `check_source` runs
  `-check` on an entry's text and returns its findings (`Finding`, a
  `util.Diag` plus whether it is a warning) instead of printing them. `-check`
  prints that list and the server publishes it, so there is one pipeline, not
  a copy. A file:// document is checked as the file it names, so its imports
  resolve from disk beside it. A finding inside an imported module belongs to
  that module, as in native's workspace mode, and is published only when that
  module is itself the document checked.
- **Formatting is `-fmt`'s** (`format_source`). The server refuses an edit
  under `-fmt -w`'s rule, `fmt_output_is_readable`: no edit when the formatted
  text would not come back through the formatter as itself.
- A publish goes out only when a document's diagnostics change, and didClose
  clears them. The exit codes are the spec's: 0 for `exit` after `shutdown`,
  1 for `exit` without it, and 2 for input that cannot be framed.

Measured on a 4-core x86-64 container: a one-function document with no
imports re-checks in about 1 ms. One importing `std/string` takes about 280 ms
per change (2026-10-03), and about 285 ms in native fern-lsp (2026-10-04),
which runs the same gates and then loads and checks the program a second time
for its cursor features. Most of either is re-loading and re-checking the
stdlib closure on every edit, so caching parsed imports between checks is the
first step `IDE-COMPILATION-RESEARCH.md` describes. Peak RSS stays at 30–31 MB over 100 changes, so each check's memory
is reclaimed.

`internal/testing/e2ecompiler/self_host_lsp_test.go` gates it (`TEST-GATES.md` has the
row). It holds the server to two references:

- **`-check`, across the whole conformance corpus.** Every finding in the
  document is published, at its position, with its code and message, and
  nothing else is.
- **native fern-lsp, on a curated corpus.** That corpus covers every placement
  shape: non-ASCII before a position, CRLF, a positionless refusal, a
  top-level const, sibling and stdlib imports, a visibility error in a
  sibling, document sync, and formatting. Native's diagnostics are its
  `fern -check`'s too, so the two servers differ only where the two checkers
  do; the corpus is curated to keep those out, and the checker differentials
  track them.

What the next slices add:

1. Open sibling buffers taking precedence over disk, and publishing findings to
   every open module a check touched, as native's workspace mode does.
2. Hover, go-to-definition and completion, over the self-host checker's types.
3. Imports parsed once and reused across checks.
4. The playground's in-process server (`fernLsp`) on the self-host wasm build,
   which is what `cmd/fern-wasm` still carries.

## Goal

Ship a Language Server Protocol implementation for `fern` and surface it in
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
- `internal/syntax/diag` defines structured error interfaces (`Positioned`,
  `Spanned`, `Hinted`) that map 1:1 onto LSP `Diagnostic` fields.
- `cmd/fern-wasm` already builds the compiler to `GOOS=js GOARCH=wasm` and
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

### LSP server — `cmd/fern-lsp/main.go` (new)

A thin Go binary that:

1. Reads JSON-RPC over stdin/stdout per the LSP spec.
2. Maintains `map[uri]string` of open documents.
3. On `didOpen` / `didChange`: loads the program and runs `fern -check`'s
   front end on it (`internal/tools/gates.Check`), translates its errors and
   warnings into `PublishDiagnosticsParams`, and sends the notification. The
   cursor features below read a second load, type-checked but not folded:
   the const fold strips the consts that symbols and references look up.
4. On `textDocument/hover`: walks the AST, finds the node at the position,
   returns the type from `Info.VarTypes` (or the AST-attached type field).
5. On `textDocument/definition`: looks up identifiers in `Info` tables.
6. On `textDocument/completion`: enumerates `Info.FuncSigs`, locals from
   `Info.Locals`, struct/enum names, plus keywords.

Hand-roll the small subset of the LSP wire format we need (≈10 message
types for an MVP) rather than depend on `go.lsp.dev/protocol`. Cuts a
dependency and keeps the wasm build small.

### Playground wiring

Same wasm binary, in-process LSP. Extend `cmd/fern-wasm/main.go` to export
a second global `fernLsp(jsonString) -> jsonString`. The browser drives the
editor, posts JSON-RPC messages synchronously into wasm, and gets
diagnostics / hover back. No worker, no server, no protocol mismatch.

Replace the `<textarea>` in `web/index.html` with **CodeMirror 6**
(~150 KB vs Monaco's ~2 MB; lint, hover, autocomplete extensions ship
separately so we only pay for what we use). Keep the existing `Run`
button and `fernInterpret` flow untouched.

## PR ordering

Each step landed as a separate commit on the same branch.

1. **AST `Walk` visitor.** ✓ `internal/syntax/ast/walk.go` —
   depth-first, source-order traversal with stop-descent
   semantics. Top-level decls without a `Pos()` got one. End-
   position threading on AST nodes was deferred: hover / def
   work from name length + the existing `Spanned` interface on
   errors, and only two niche features (type-annotation hover,
   field-access hover) need real end positions. Future PR.

2. **`cmd/fern-lsp` MVP.** ✓ `internal/tools/lsp` +
   `cmd/fern-lsp` — hand-rolled JSON-RPC wire format (no
   third-party deps so the wasm build stays slim). Handles
   `initialize` / `shutdown` / `exit`, full-sync `didOpen` /
   `didChange` / `didClose`, and publishes parser + checker
   diagnostics. Same `Server` type drives stdio and the wasm
   wrapper via `HandleMessage` + `SetPublisher`.

3. **`hover` + `definition`.** ✓ Single `findNameAt` helper
   reused by both — finds the deepest `Ident` / `StructLit`
   under the cursor, then resolves it through `checker.Info`.
   Bare enum variants (parser emits Ident, checker resolves
   via `variantOf` without rewriting the AST) get a fallback
   `lookupVariant` sweep.

4. **Playground hookup.** ✓ `cmd/fern-wasm` exposes
   `fernLsp(json)` + `fernLspOnNotify(cb)`; the same
   `internal/tools/lsp.Server` runs in-process. `web/index.html`
   keeps the textarea (no CodeMirror swap — out of scope for
   this PR) and gains a Problems panel + click-for-type
   cursor strip. CodeMirror migration is now an independent
   follow-up — the `fernLsp` wire is stable, swapping the
   editor on top of it is mechanical.

5. **`completion` + `signatureHelp`.** ✓ Completion
   enumerates locals + params + top-level decls + variants +
   keywords; client filters. Signature help scans source text
   backward from the cursor for the nearest unmatched `(`,
   counting commas for the active-param index. String /
   comment-aware so a `,` inside `"a, b"` doesn't fool it.

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
