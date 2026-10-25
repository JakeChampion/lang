# The language server: `fern -lsp`

Status: [reference] shipped as `fern -lsp`.

`fern -lsp [-target T] [-embed DIR] [stdlib-root]` runs the compiler as a
language server on stdin and stdout (#6641): diagnostics, formatting, hover,
go-to-definition, completion, signature help, document symbols, inlay hints,
references, rename, semantic tokens and code actions. The shipped `fern`
carries it with its stdlib; the VS Code extension (`editors/vscode`) spawns
it, and the browser playground runs it as `playground.wasm -lsp` (below).
Post-MVP performance work is surveyed in `IDE-COMPILATION-RESEARCH.md` and
tracked in [#4415](https://github.com/JakeChampion/lang/issues/4415).

## The server

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
- **Formatting is `-fmt`'s** (`format_text`: a `.fern.md` document's chunk
  bodies, or the whole source). A `.fern` document that does not parse gets
  no edit; its published diagnostics carry the parse errors `-fmt` reports.
  The server refuses an edit under `-fmt -w`'s rule, `fmt_output_is_readable`:
  no edit when the formatted text would not parse, would not come back through
  the formatter as itself, or loses a comment or a declaration of the source.
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
  ways. The hover text reads `(var) x: i32`,
  `function add(i32, i32): i32`, `struct Point { x: i32, y: i32 }`,
  `(variant of Color) Red`, `(method on Point) …` or `(from util) …`, with
  another module's types spelled as the importing module writes them
  (`util.Point`, or `u.Point` under `import "./util" as u`). Hover
  covers constants, a lambda's parameters and enum-qualified variants too. A
  definition lands on the declared name; one into another module names that
  module's file, one into the stdlib the stdlib file under the root; and a
  name with no declaration (`Some`) has none.
- **Completion, signature help, document symbols and inlay hints**
  (`compiler/lspassist.fern`) read the same view, tokens and scopes.
  Completion is asked again after `.`, signature help after `(` and `,`.
  Completion offers the enclosing function's bindings — every one, a loop's,
  an arm's and a lambda's included, typed where it is bound, and none once
  the cursor is past the function's body — then the module's functions,
  constants, structs, enums and variants, `Option` and `Result`, and the
  keywords, sorted by label.
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
  requested range. Signature help covers methods and other modules'
  functions, names a parameter by its offsets rather than by text (which
  `add(i32, i32)`'s two parameters share), and gives none inside brackets in
  the arguments. An outline entry spans its whole declaration with its name
  selected, holds methods and no built-in structs, and gives a field and a
  destructured name their own positions. A hint names an imported type as
  the document writes it (`util.Kind`). A literate document has an outline
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
  in both, and a rename edits each file; they reach no module that check did
  not load. A declaration is placed on its name, and a request on a
  declaration's own name answers; a local is the binding a name reads, not
  every like-named local of the function, and a parameter's declaration is one
  of its places; `includeDeclaration` is honoured; and a rename to a name that
  is not an identifier, or of something with no declaration (`Some`) or
  declared in the stdlib, is refused (null).
- **Semantic tokens** (`compiler/lsptokens.fern`) use an eleven-type legend
  with no modifiers. Each name a module writes is classified from the tree
  (identifiers by the checker's tables, members, struct literals and their
  fields, assignment targets, arm variants), then each declaration's name from
  the outline, then the types an annotation writes from the tokens, first
  classification winning. Declarations, parameters and bindings are
  classified where they are declared, a built-in function as a function and a
  type parameter as a type; what cannot be classified is left out, and
  `keyword` is never emitted.
- **Code actions** are quick fixes from a diagnostic's `data`. The checker
  attaches a fix (`util.Diag.fix`, which `-check` prints as a `help:` line) to
  E001 for a name read, called or assigned that resolves to nothing, respelt
  as the closest name in scope or the replacement of a retired one, and to
  E043 for a struct literal's, an access's, a struct pattern's or a
  destructuring `let`'s unknown field or method, respelt as the closest field
  or method. A fix replaces the first identifier spelled as the old name from
  its position on along its line: a pattern's fields have no position of their
  own, so their fix stands where the arm or the `let` does and the server
  places it on the field. A code action names the diagnostic as the client
  sent it.
- A publish goes out only when a document's diagnostics change, and didClose
  clears them. The exit codes are the spec's: 0 for `exit` after `shutdown`,
  1 for `exit` without it, and 2 for input that cannot be framed.

Measured on a 4-core x86-64 container: a one-function document with no
imports re-checks in about 1 ms. One importing `std/string` takes about 280 ms
per change (2026-10-03). Most of it is re-loading and re-checking the stdlib
closure on every edit, so caching parsed imports between checks is the first
step `IDE-COMPILATION-RESEARCH.md` describes. Peak RSS stays at 30–31 MB over
100 changes, so each check's memory is reclaimed.

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
rename, semantic tokens and code actions, every answer written out as data.
The diagnostics are held to:

- **`-check`, across the whole conformance corpus.** Every finding in the
  document is published, at its position, with its code and message, and
  nothing else is.
- **A curated corpus of placements.** Non-ASCII before a position, CRLF, a
  positionless refusal, a top-level const, sibling and stdlib imports, a
  visibility error in a sibling, document sync, and formatting, each answer
  written out. They are the answers the Go language server gave for the same
  documents before it was deleted, on 2026-10-15.

## In the browser

The playground runs the same server: `compiler/playground_run.fern`'s `-lsp`
mode calls `fern.serve_lsp_in_memory`, which reads the stdlib from the
driver's embedded bundle as files under `/stdlib` instead of from disk. A
page hands a WASI instance its stdin as one buffer and gets nothing back until
it exits, so the page (`web/index.html`) replays a whole session per question
on its own `web/driver-worker.js`: initialize, open the document with the
editor's text, ask — a hover, a completion, or nothing when it only wants the
diagnostics — then shut down. Every session publishes the document's
diagnostics, so any answer refreshes the Problems panel too. The page has no
filesystem, so `web/wasi-shim.js` answers every path with EBADF and the
server's manifest probes find nothing, as under `wasmtime` with no `--dir`.

Replaying the protocol, rather than a request mode of the driver's own, means
the page and an editor get their answers from one implementation through one
wire; the cost is that every question re-checks the document. Measured in
Node 22 through `web/wasi-shim.js` (2026-10-15): a session for a program
importing `std/i32` takes about 0.35 s, one importing `std/platform` about
3.6 s. Carrying the server adds 0.4 MB to `playground.wasm` (11.6 MB).

What the next slices add:

1. Imports parsed once and reused across checks.
2. Completion's bindings are the whole enclosing function's; offering only
   those in scope at the cursor needs statement extents the syntax tree does
   not record. A rename edits only the modules the requesting document's check
   loaded.
