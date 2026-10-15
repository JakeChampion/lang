# LSP integration plan

> **Status: MVP shipped** — `cmd/fern-lsp` exists (diagnostics, hover,
> go-to-def, completion) and the playground wires it via `cmd/fern-wasm`.
> Post-MVP performance work is surveyed in `IDE-COMPILATION-RESEARCH.md`
> and tracked in [#4415](https://github.com/JakeChampion/lang/issues/4415).
> The self-host compiler serves every method the Go server does — diagnostics,
> formatting, hover, go-to-definition, completion, signature help, document
> symbols, inlay hints, references, rename, semantic tokens and code actions —
> as `fern -lsp` (#6641); see the next section. Everything below it describes
> the Go server.

## The self-host server: `fern -lsp`

`fern -lsp [-target T] [-embed DIR] [stdlib-root]` runs the self-host CLI as a
language server on stdin and stdout: diagnostics, formatting, hover,
go-to-definition, completion, signature help, document symbols, inlay hints,
references, rename, semantic tokens and code actions.

- **The wire** is `compiler/lsp.fern`. It does Content-Length framing
  over `stdin()`, JSON-RPC through `std/json`, the UTF-16 position conversion
  (#8468) and file:// URIs to paths. It knows nothing of the compiler.
- **Diagnostics are `-check`'s findings.** `fern.fern`'s `check_source` runs
  `-check` on an entry's text and returns its findings (`Finding`, a
  `util.Diag` plus whether it is a warning) instead of printing them. `-check`
  prints that list and the server publishes it, so there is one pipeline, not
  a copy. A file:// document is checked as the file it names, so its imports
  resolve beside it.
- **Workspace mode.** Every open file:// document stands in for its file on
  disk wherever an import reaches it, a manifest dependency's modules
  included (`modloader.Buffers`). A finding belongs to the module its
  position is numbered in, a literate module's to its document. A check
  publishes the findings in every open module it loaded, not only the
  document that changed, so a module whose import changed under it is
  brought up to date at the next check that loads both. An open document
  the check did not load keeps what its own check published.
- **Formatting is `-fmt`'s** (`format_source`). The server refuses an edit
  under `-fmt -w`'s rule, `fmt_output_is_readable`: no edit when the formatted
  text would not come back through the formatter as itself.
- **Hover and go-to-definition** (`compiler/lspquery.fern`) answer from what
  the document's last check loaded, kept on the server: the bundle and each
  module's text. The first request after a check builds the checker's view of
  the bundle (`checker.query_view`: the module as `check_module` types it and
  its tables); later requests reuse it. The name under the cursor is found
  among the module's tokens and read off the syntax tree at that token's
  position — an identifier, a qualified `alias.name`, a field or method after a
  `.`, a struct literal — or, where no node is, as a type written in an
  annotation. Its type comes from the checker's scope at that statement, walked
  with `bind_stmt` through every enclosing block, arm, loop and lambda, and a
  local is told from a global by `lexical.resolve_func`. Only the requesting
  document's own module is searched, so a sibling's name at the same position
  never answers. A literate document's positions go through its line map both
  ways. The hover text is the Go server's shape (`(var) x: i32`,
  `function add(i32, i32): i32`, `struct Point { x: i32, y: i32 }`,
  `(variant of Color) Red`, `(method on Point) …`, `(from util) …`), with
  another module's types under their bundled names (`util__Point`), as native's
  are. A definition into another module names that module's file. It differs
  from the Go server's in three ways: it lands on the declared name, not on the
  declaration's keyword; one into the stdlib names the stdlib file under the
  root, where the Go server named the requesting document at the stdlib line;
  and a name with no declaration (`Some`) has none, where the Go server
  answered 0:0. Hover also describes what the Go server left blank: a
  constant, a lambda's parameter and an enum-qualified variant.
- **Completion, signature help, document symbols and inlay hints**
  (`compiler/lspassist.fern`) read the same view, tokens and scopes. The
  capabilities are the Go server's: completion is asked again after `.`,
  signature help after `(` and `,`. Completion offers the enclosing function's
  bindings — every one, typed where it is bound, as the Go server offers every
  `let` of the function — then the module's functions, constants, structs,
  enums and variants, `Option` and `Result`, and the keywords, sorted by label.
  After a `.` it offers what can follow instead: a module's exports after its
  alias, an enum's variants, or the fields and methods (declared, built-in and
  std/array's) of the value before it, typed from the tree or, while the access
  does not parse yet, from the binding it names. Signature help finds the call
  from the tokens before the cursor, so strings and comments are skipped
  exactly, and resolves its callee as hover does: a function, a module's
  function or a method; a call that does not parse falls back to the function
  its spelling names. The outline lists the document's own functions, methods,
  structs with their fields, enums with their variants, and constants. An inlay
  hint is the type of each `let` that writes none, for the functions in the
  requested range. The answers are the Go server's where it has one, except:
  completion leaves out a function the cursor is past, and offers constants
  and a loop's, an arm's and a lambda's bindings; after a `.` the Go server
  offers the whole scope again; signature help also covers methods and other
  modules' functions, names a parameter by its offsets rather than by text
  (which `add(i32, i32)`'s two parameters share), and gives none inside
  brackets in the arguments; an outline entry spans its whole declaration with
  its name selected, where the Go server gave the name's length at the
  keyword, and holds methods and no built-in structs; a field and a
  destructured name have their own positions; a hint names an imported type as
  the document writes it (`util.Kind`); and a literate document has an outline
  and hints.
- **References and rename** (`compiler/lsprefs.fern`) resolve the name at
  the position, or the declaration whose name it is, to what it names: a
  local binding (by `lexical.resolve_func`'s symbol, so shadowing is
  respected), a module-level function or constant, a struct or enum, a
  variant, a field or a method. Each kind is found where the tree has it — an
  identifier, a member after a `.` typed by the scope its statement is checked
  in, a struct literal's field (the parser now records each field name's
  position), a variant at any depth of a match arm's, `if let`'s or
  `let … else`'s pattern (the parser flattens a nested pattern into matches
  that all stand where the whole pattern does, so the pattern's tokens are
  searched) — and placed on the token spelling it, so a node synthesised at a
  borrowed position never answers. A type is found among the tokens of each
  module that can name it, bare or behind an alias. References cover every
  module the document's check loaded, so a name from another module is found
  in both, and a rename edits each file; like the Go server's, they reach no
  module that check did not load.
  The answers differ from the Go server's where it was wrong: a declaration
  is placed on its name, not at its keyword (a rename edited `fun`); a request
  on a declaration's own name answers; a local is the binding a name reads, not
  every like-named local of the function, and a parameter's declaration is one
  of its places; a variant inside a nested pattern is found, where the Go
  server found one only heading an arm; `includeDeclaration` is honoured; and a
  rename to a name that
  is not an identifier, or of something with no declaration (`Some`) or
  declared in the stdlib, is refused (null).
- **Semantic tokens** (`compiler/lsptokens.fern`) use the Go server's
  eleven-type legend and its encoding. Each name a module writes is classified
  from the tree (identifiers by the checker's tables, members, struct literals
  and their fields, assignment targets, arm variants), then each declaration's
  name from the outline, then the types an annotation writes from the tokens,
  first classification winning. The Go server classified only what a function
  body names and each annotated type, and called whatever it could not
  classify a variable; this one classifies declarations, parameters and
  bindings where they are declared, a built-in function as a function and a
  type parameter as a type, and leaves out what it cannot classify. Neither
  emits `keyword`.
- **Code actions** are quick fixes from a diagnostic's `data`. The self-host
  checker attaches a fix (`util.Diag.fix`, also printed by `-check` as the
  Go renderer's `help:` line) to the diagnostics the Go checker does: E001 for
  a name read, called or assigned that resolves to nothing, respelt as the
  closest name in scope or the replacement of a retired one, and E043 for a
  struct literal's or an access's unknown field or method, respelt as the
  closest field or method. The server publishes a fix only when the text at
  its span is the name it replaces, which drops the one the checker reports
  for a struct pattern's field at the arm's start (the Go server published an
  edit there that broke the pattern), and a code action names the diagnostic
  as the client sent it. The Go checker's other E043 fix, a destructuring
  `let`'s unknown field, has no self-host diagnostic.
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

A hover or definition costs, on the same container (2026-10-15): for a
document importing `std/json` and `std/string`, the first request after a
check, which builds the view, is lost in the few milliseconds of run-to-run
noise, and each later one takes under 0.1 ms. On the 2,700-line
`compiler/flatten.fern` one takes about 1 ms, and on the 29,000-line
`compiler/checker.fern` about 18 ms, most of it re-lexing the module. The other
requests cost about the same there (2026-10-15): completion inside a function
with 18 bindings, 1,213 items, about 22 ms; signature help, the whole outline
and the whole file's inlay hints about 20 ms each. References and rename cost
60–70 ms there — `Scope`'s 520 places, `dg_at`'s 245, a field's 12 — and
the whole file's semantic tokens, 60,174 of them, about 135 ms.

`internal/testing/e2ecompiler/self_host_lsp_test.go` gates it (`TEST-GATES.md` has the
row), with `self_host_lsp_hover_test.go` for hover and definition,
`self_host_lsp_complete_test.go` for completion, signature help, document
symbols and inlay hints, and `self_host_lsp_refs_test.go` for references,
rename, semantic tokens and code actions: the Go server's cases, their answers
written out as data. The diagnostics are held to two references:

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

1. Imports parsed once and reused across checks.
2. The playground's in-process server (`fernLsp`) on the self-host wasm build,
   which is what `cmd/fern-wasm` still carries; then the Go server's deletion.
3. Completion's bindings are the whole enclosing function's, as the Go
   server's are; offering only those in scope at the cursor needs statement
   extents the syntax tree does not record. A rename, as the Go server's, edits
   only the modules the requesting document's check loaded.

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

Same wasm binary, in-process LSP. `cmd/fern-wasm/main.go` exports a global
`fernLsp(jsonString) -> jsonString`, called in `web/lsp-worker.js`: the page
posts JSON-RPC messages to that worker and gets diagnostics / hover back as
messages. No server, no protocol mismatch. The worker exists because a check
type-checks every module the program imports, seconds in wasm for anything
importing `std/platform`, and on the page's thread that froze the editor.

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
