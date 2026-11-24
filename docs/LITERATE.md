# Literate programming in Fern

Status: [reference] shipped.

Fern supports **Knuth-style literate programming**. You write a program as
a document for humans to read. The code is in named *chunks*, and you put
the chunks in the order that best explains the ideas. A tool then *tangles*
the chunks into a program that compiles, and *weaves* a typeset reading
copy.

> *"Instead of imagining that our main task is to instruct a computer
> what to do, let us concentrate rather on explaining to human beings
> what we want a computer to do."* — Donald Knuth

A literate Fern program is a Markdown file with the extension `.fern.md`.
Because the carrier format is Markdown, GitHub and any Markdown viewer
already show the file as documentation, with no other tool. `weave` only
adds chunk labels and cross-references.

## The chunk syntax

Code is in fenced code blocks tagged `fern`. A block whose first non-blank
line is a **chunk header** `<<NAME>>=` defines the chunk `NAME`. The lines
after the header are its body:

````markdown
```fern
<<greeting>>=
function greeting(): string { return "hello, literate world"; }
```
````

The **root chunk** is named `*`. Tangling expands `<<*>>` to produce the
program, so every literate program has one:

````markdown
```fern
<<*>>=
<<greeting>>
function main(): i32 { print(greeting()); return 0; }
```
````

Inside a chunk body, a line whose trimmed content is exactly `<<NAME>>`
(no trailing `=`) is a **chunk reference**. Tangling replaces it with
the expansion of `NAME`. The reference above pulls in `greeting`.
Chunks resolve through a name table, not by document order. Thus you can
define `greeting` *before or after* the place where you use it.

Four more rules complete the syntax:

- **Indentation is preserved.** A reference indented by *n* columns
  prepends that indentation to every line of the expansion, so a chunk
  referenced inside a block stays aligned. (Classic `noweb` behaviour.)
- **Definitions accumulate.** Defining the same `NAME` more than once
  *appends* to the chunk in document order — the way you grow a
  definition across a narrative (woven as `⟨name⟩+≡`).
- **Headerless `fern` blocks are display-only.** A ```` ```fern ````
  block with no `<<NAME>>=` header is woven into the document but never
  tangled. Thus you can show example snippets that are not part of the
  build.
- **Escaping.** A line you want to emit *literally* as `<<name>>`
  (rather than expand) is written with a leading backslash —
  `\<<name>>`. Tangling removes the backslash and emits `<<name>>`
  verbatim, and the chunk does not have to exist. Weaving also shows it
  literally.
  Useful for code/templates that themselves contain `<<…>>` tokens.

## The CLI

```sh
fern -tangle prog.fern.md     # write the tangled Fern source to stdout
fern -weave  prog.fern.md     # write a cross-referenced Markdown reading copy
```

Both weaves end with a **chunk index** appendix — every chunk listed
with the chunks that reference it (noweb's identifier index), the root
and any unused chunks marked. (Omitted for a trivial one-chunk
document.)

Both accept `-o` to write to disk instead of stdout. For a multi-file
document (`file=` blocks), `-tangle -o DIR` writes one file for each module
under `DIR`, and makes the subdirectories. Thus you can examine or build the
generated tree directly. A document with a single `<<*>>` root writes
`-tangle -o FILE`. `-weave -o FILE` writes the woven Markdown.

A `file=` name is a path below the output directory. An absolute path
or a name with a `..` segment is an error (`E092`) at the fence line.

```sh
fern -tangle -o gen/ prog.fern.md    # eject each module under gen/
fern -tangle -o prog.fern one.fern.md
fern -weave  -o prog.md  prog.fern.md
```

`fern -tangle -chunk NAME` expands and prints only one named chunk and its
transitive references, not the `<<*>>` root. Use it to examine or extract
one chunk:

```sh
fern -tangle -chunk 'the main loop' prog.fern.md
```

`fern -weave -html` renders a **self-contained, styled HTML page**
instead of Markdown — embedded CSS, light Fern syntax highlighting, and
clickable `<<chunk>>` cross-reference links that jump to each chunk's
definition (combine with `-o page.html`):

```sh
fern -weave -html -o prog.html prog.fern.md
```

A small Markdown subset renders the prose: headings, lists, blockquotes,
rules, and the inline spans `code`, **bold**, *italic* and links. The value
over the Markdown weave is the linked, highlighted code chunks, not a full
CommonMark engine. The page also gets a
**table of contents** (from the document's headings, which are anchored)
and a **chunk index** appendix listing every chunk alphabetically —
each links to its definition and to the chunks that reference it
(noweb's identifier index), with the root and any unused chunks marked.

`fern -fmt` formats the Fern code inside a document's chunks in place,
leaving prose, fences, and chunk headers untouched (`-w` writes back,
`-d` shows a diff):

```sh
fern -fmt -w prog.fern.md
```

The formatter changes a chunk body when the body parses on its own, as a set
of top-level declarations or as a statement list. It leaves a body
**verbatim** when the body does not parse alone. Examples are a fragment cut
in the middle of a construct, and a chunk with `<<reference>>` lines, which
are not valid Fern. Thus formatting never corrupts a document. Comments and blank-line
grouping inside a chunk are preserved.

A `.fern.md` file is also accepted directly by every mode that takes a
`.fern` file — it is tangled in memory first:

```sh
fern -interp prog.fern.md            # tangle, then run through the interpreter
fern -check  prog.fern.md            # tangle, then type-check
fern -o prog prog.fern.md            # tangle, then compile + link
fern -run    prog.fern.md            # tangle, compile, and run
```

Disk-relative `import` paths inside a literate file resolve against the
document's own directory, exactly as they would for a `.fern` file in
that location.

## Diagnostics map back to the document

Tangling reorders chunks, so a line in the generated source rarely
shares the line number of the line you wrote. Fern tracks the
provenance of every generated line and **remaps compiler diagnostics
back onto the `.fern.md` document** — the file name, line, column, and
the rendered source snippet all point at what you typed, not at the
generated source. The implementation map below tells how.

## Editor support (LSP)

`fern -lsp` diagnoses `.fern.md` documents: it tangles the source,
type-checks the generated Fern, and **remaps the diagnostics back onto
the document** so errors are reported on the line you wrote — including
tangle errors (missing root, undefined / cyclic chunk). Open a
`.fern.md` in an editor wired to `fern -lsp` and a type error in a chunk
squiggles in place.

The **cursor-driven features also work**: hover, go-to-definition,
find-all-references, completion, and signature help. The server moves a
request position from the document onto the generated source, and queries
the tangled program. Then it moves each result range back onto the
document. Thus go-to-definition on a chunk reference goes to the definition
line of the chunk, and references span each use across chunks. A cursor on
a line that is not tangled, such as prose, a chunk header or an unused
chunk, returns nothing. The whole-document features — document symbols,
inlay hints, semantic tokens and rename — cover every chunk the same way.

## Doctests — runnable examples

A ```` ```fern test ```` block is a *doctest*. `fern -doctest` tangles the
example into a standalone program, compiles it, and runs it. Its `<<refs>>`
expand against the chunks of the document, so an example can pull in the
code that the prose explains. The example **passes when it compiles and
`main` returns 0**. Thus it cannot rot silently when the code around it
changes.

````markdown
```fern test name=greeting-says-hi
<<greeting>>
function main(): i32 {
    if (greeting() != "hello") { return 1; }
    return 0;
}
```
````

```sh
fern -doctest prog.fern.md
```

The output is TAP (`ok N - name` / `not ok N - name`). The command exits
non-zero if an example fails. An optional `name=…` directive labels the
example. If there is no name, the example has a number. A compile error in
an example points at the document line that you wrote. The example resolves
stdlib and disk-relative imports against the directory of the document. The
woven output shows a `test` block, but the block is never part of the
tangle.

## Lints

- **Unused chunk** — a chunk defined but never reached from a tangle
  root (the `<<*>>` root, or any `file=` file-root) is reported as a
  non-fatal warning `W003` on stderr, at the chunk's first header
  (`DOC:LINE:1: warning[W003]: chunk <<name>> is defined but never
  used`). Reachability-based, so an entire dead subtree surfaces — it
  most often means a typo in a `<<reference>>`. Compilation still
  proceeds.

## Errors specific to tangling

- **No root chunk** — the document defines no `<<*>>=` block.
- **Undefined chunk reference** — `<<NAME>>` references a chunk that is
  never defined. The error is at the reference.
- **Cyclic chunk reference** — a chunk is defined in terms of itself
  (directly or transitively).

## Multiple modules from one document

A single document can tangle to **several** Fern modules. A fern fence
with a `file=PATH` directive is a *file-root*: its body becomes the
module `PATH`, expanding any `<<ref>>` lines just like the `<<*>>` root.

````markdown
```fern file=main.fern entry
import "./mathx";
function main(): i32 { return mathx.square(6); }
```

```fern file=mathx.fern
<<square>>
```

```fern
<<square>>=
pub function square(n: i32): i32 { return n * n; }
```
````

Rules:

- **The whole document shares the chunk definitions.** Each file-root pulls
  in the chunks that it references. So you describe the
  architecture as one narrative and partition it into modules with
  `file=` blocks.
- The generated modules **`import` each other normally** (`import
  "./mathx"`), resolved against the document's directory. The whole set
  is tangled in memory, so nothing is written to disk for a plain
  compile / `-interp` / `-check`.
- **Same `file=PATH` repeated** concatenates in document order, exactly
  like same-name chunk pieces.
- The **compile entry** is the file marked `entry`. With no mark, it is the
  only file, or else the only module that defines a `main` function. Mark
  one block `file=app.fern entry` to be explicit.
- **`fern -tangle`** prints each module under a `// ==> path <==` banner.
  **`fern -weave`** labels each file-root `📄 path`.
- **Diagnostics still map back to the document**: each module carries
  its own provenance map, so a type error in any generated file points
  at the `.fern.md` line you wrote.

A document either uses `file=` blocks (multi-module) or the single
`<<*>>` root (one module) — `<<*>>` is the shorthand for the common
one-file case.

## Example

[`examples/literate/fizzbuzz.fern.md`](../examples/literate/fizzbuzz.fern.md)
is a complete single-file program. It defines its chunks out of order,
references a chunk inside an indented `while` body, and reads as prose from
top to bottom.
[`examples/literate/multi_module.fern.md`](../examples/literate/multi_module.fern.md)
is a two-module program that `file=` roots assemble.

## A literate document as an importable library

A `.fern` file, or another `.fern.md`, can `import` a literate document as
an ordinary module. Write `import "./greet"`. When there is no plain
`greet.fern`, the compiler tangles a sibling `greet.fern.md` in memory and
loads it in its place:

```fern
import "./greet";   // resolves to greet.fern, or greet.fern.md
function main(): i32 { return greet.greeting(); }
```

The document is tangled like any single-root program, so its `pub` decls
are the module's exported surface. A plain `greet.fern` always wins over
a same-named `greet.fern.md`. A diagnostic in the imported library is
reported against *its* document, at the line you wrote there — the same
remap the entry document gets. A **multi-file** (`file=`) document has no
single importable module, so importing one is an error (compile it as an
entry, or import a specific generated `.fern` instead).

## Standard library modules

Standard library modules can use `.fern.md` sources. Imports retain their
public names, such as `import "std/option"`. The compiler expands the
document in memory before it checks or compiles the module.

Most of the library is literate. The
[source inventory](LITERATE-STDLIB.md) lists the documents and the plain
sources. Each literate module contains STE explanations and named chunks,
and many contain executable examples. Brief declaration comments give the
text for API reference pages and editor hovers.

```sh
fern -doctest internal/stdlib/std/option.fern.md
fern -weave -html -o option.html internal/stdlib/std/option.fern.md
fern -doc internal/stdlib/std/option.fern.md
```

The Go loader retains the logical `.fern` module key and the document's
source map. Module discovery includes both source formats. The build,
embedded library, and standalone import checks also include `.fern.md`
files. A plain `.fern` module takes precedence when both formats exist.

## Scope and current limitations

- The chunk grammar uses the `<<name>>` delimiters from `noweb`/WEB.
  Write a whole-line literal `<<name>>` as `\<<name>>` (see Escaping
  above). A `<<` or `>>` at another place on a line is never a marker.

## Implementation map

The engine is `compiler/literate.fern`:

- `parse` splits a document into blocks: prose, display-only, chunk, `file=`
  root and `test` example.
- `tangle` expands the root chunk `<<*>>` into Fern source and a provenance
  map for each line. `tangle_files` does the same for each `file=` root.
- `entry_file` selects the compile entry.
- `doctests` tangles each example on its own.

 `compiler/weave.fern`
renders the blocks for `-weave`: Markdown, or with `-html` a page whose prose
goes through its small Markdown renderer. `-tangle`, `-weave` and `-doctest`
are `run_tangle` and `run_doctests` in `compiler/fern.fern`. The CLI loads,
gates and interprets a doctest as `-interp` runs a program.

The compiler tangles for every `-target` compile, and for its own
`-check` / `-interp`:
- `entry_sources` in `compiler/fern.fern` tangles a `.fern.md` entry to its
  module, or to each `file=` module with the entry first.
- `modloader.read_module` uses a `.fern.md` sibling for an import when there
  is no `.fern` file.
- The `util.LineMap` of each module moves a diagnostic onto its document
  when the CLI prints it (`util.doc_diag`).

 A `-g` build moves each line row the same way
(`asmcore.loc_row`). The remap is there and not in the lexer. The
compiler orders and identifies expressions by position, and a chunk can
expand two times.

The Go front end has its own tangle in `internal/tools/literate`. The loader
of the oracle and the Go language server read documents through it. The
`-tangle`, `-weave` and `-doctest` of `cmd/fern` run the tangle of the
compiler. On the Go side
(`cmd/fern/main.go`), `loadEntry` tangles a `.fern.md` entry in memory before
the `-check` / `-interp` pipeline. `loadMultiFileEntry` gives each generated
module to `modload.LoadWith` as a virtual-file override, keyed by its path
relative to the directory of the document. The load starts at the entry.

An import of a literate library goes through `modload.readModuleSource`. When
a `.fern` import target is missing, it uses a sibling `.fern.md` and tangles
it. `LoadWithLiterate` returns the per-module `LiterateModule{DocPath, DocSrc,
LineMap}` provenance, so the CLI's `entry.remaps` (a per-module
`litRemap{docPath, docSrc, remap}`) maps an imported library's diagnostics onto
*its* document. Diagnostics are rendered through `diag.FormatRemapped`, the
literate-only sibling of `diag.Format`.

## Maintaining this

Coverage lives in `compiler/literate.fern`'s own `main` (run by
`TestSelfHostLiterate*`), `internal/testing/e2ecompiler/self_host_literate_cli_test.go`
(`-tangle`, `-doctest`, and `-weave` against the goldens in `testdata/weave/`),
`internal/tools/literate/*_test.go` for the Go tangle, the `diag`
`FormatRemapped` tests, and `internal/testing/e2e/literate_test.go` +
`literate_multifile_test.go` (the single- and multi-file diagnostic-remap
contracts). Examples:
`examples/literate/fizzbuzz.fern.md` (single root) and `multi_module.fern.md`
(multi-file).

When extending the chunk grammar or the remap, add cases at the layer you
touched. **The diagnostic remap — generated line → document line — is the most
regression-prone surface here**, so it is the one to cover first.
