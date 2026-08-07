# Literate programming in Fern

Fern supports **Knuth-style literate programming**: you write a program
as a document for humans to read, with the code embedded in named
*chunks* that can be presented in whatever order best explains the
ideas. A tool then *tangles* the chunks into a compilable program and
*weaves* a typeset reading copy.

> *"Instead of imagining that our main task is to instruct a computer
> what to do, let us concentrate rather on explaining to human beings
> what we want a computer to do."* — Donald Knuth

A literate Fern program is a Markdown file with the extension
`.fern.md`. Because the carrier format is Markdown, the file already
renders as documentation on GitHub and in any Markdown viewer with no
extra tooling — `weave` only adds chunk labels and cross-references on
top.

## The chunk syntax

Code lives in fenced code blocks tagged `fern`. A block whose first
non-blank line is a **chunk header** `<<NAME>>=` defines the chunk
`NAME`; the remaining lines are its body:

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
the expansion of `NAME`. The reference above pulls in `greeting`, and
because chunks resolve through a name table rather than by document
order, `greeting` can be defined *before or after* the place it's used.

Three more rules round it out:

- **Indentation is preserved.** A reference indented by *n* columns
  prepends that indentation to every line of the expansion, so a chunk
  referenced inside a block stays aligned. (Classic `noweb` behaviour.)
- **Definitions accumulate.** Defining the same `NAME` more than once
  *appends* to the chunk in document order — the way you grow a
  definition across a narrative (woven as `⟨name⟩+≡`).
- **Headerless `fern` blocks are display-only.** A ```` ```fern ````
  block with no `<<NAME>>=` header is woven into the document but never
  tangled, so you can show illustrative snippets that aren't part of the
  build.
- **Escaping.** A line you want to emit *literally* as `<<name>>`
  (rather than expand) is written with a leading backslash —
  `\<<name>>`. Tangling strips the backslash and emits `<<name>>`
  verbatim (the chunk needn't exist); weaving shows it literally too.
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
document (`file=` blocks) `-tangle -o DIR` ejects one file per module
under `DIR` (creating subdirectories), so the generated tree can be
inspected or built directly; a single-`<<*>>` document writes
`-tangle -o FILE`. `-weave -o FILE` writes the woven Markdown.

```sh
fern -tangle -o gen/ prog.fern.md    # eject each module under gen/
fern -tangle -o prog.fern one.fern.md
fern -weave  -o prog.md  prog.fern.md
```

`fern -tangle -chunk NAME` expands and prints just one named chunk (and
its transitive references) instead of the `<<*>>` root — handy for
inspecting or extracting a single chunk:

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

The prose is rendered through a small Markdown subset (headings, lists,
blockquotes, rules, and the inline spans `code`, **bold**, *italic*,
links); the value over the Markdown weave is the linked, highlighted
code chunks rather than a full CommonMark engine. The page also gets a
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

A chunk body is reformatted when it parses on its own — as either a set
of top-level declarations or a statement list. Bodies the formatter
can't parse standalone — fragments split mid-construct, or chunks
containing `<<reference>>` lines (not valid Fern) — are left **verbatim**,
so formatting never corrupts a document. Comments and blank-line
grouping inside a chunk are preserved.

A `.fern.md` file is also accepted directly by every mode that takes a
`.fern` file — it is tangled in memory first:

```sh
fern -interp prog.fern.md            # tangle, then run through the interpreter
fern -check  prog.fern.md            # tangle, then type-check
fern -o prog prog.fern.md            # tangle, then compile + link
fern --run   prog.fern.md            # tangle, compile, and run
```

Disk-relative `import` paths inside a literate file resolve against the
document's own directory, exactly as they would for a `.fern` file in
that location.

## Diagnostics map back to the document

Tangling reorders chunks, so a line in the generated source rarely
shares the line number of the line you wrote. Fern tracks the
provenance of every generated line and **remaps compiler diagnostics
back onto the `.fern.md` document** — the file name, line, column, and
the rendered source snippet all point at what you actually typed, not
at the generated intermediate. (Internally: `Tangle` returns a line map
that the CLI turns into a position remapper for `diag.FormatRemapped`.)

## Editor support (LSP)

`fern-lsp` diagnoses `.fern.md` documents: it tangles the source,
type-checks the generated Fern, and **remaps the diagnostics back onto
the document** so errors are reported on the line you wrote — including
tangle errors (missing root, undefined / cyclic chunk). Open a
`.fern.md` in an editor wired to `fern-lsp` and a type error in a chunk
squiggles in place.

The **cursor-driven features work too**: hover, go-to-definition,
find-all-references, completion, and signature help. A request position
is translated from the document onto the generated source, the tangled
program is queried, and any result ranges are remapped back onto the
document — so go-to-definition on a chunk reference jumps to the chunk's
definition line, references span every use across chunks, and so on. A
cursor on a non-tangled line (prose, a chunk header, an unused chunk)
simply returns nothing. Whole-document features (semantic tokens, inlay
hints, document symbols, rename) remain inert for `.fern.md` for now.

## Doctests — runnable examples

A ```` ```fern test ```` block is a *doctest*: a runnable example that
`fern -doctest` tangles (its `<<refs>>` expand against the document's
chunks, so an example can pull in the very code the prose explains) into
a standalone program, compiles, and runs. The example **passes when it
compiles and `main` returns 0** — so it can't silently rot as the code
around it changes.

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

Output is TAP (`ok N - name` / `not ok N - name`); the command exits
non-zero if any example fails. An optional `name=…` directive labels the
example (otherwise it's numbered). A compile error in an example is
reported against the document line you wrote. The example resolves
stdlib and disk-relative imports against the document's directory; a
`test` block is shown in the woven output but never part of the tangle.

## Lints

- **Unused chunk** — a chunk defined but never reached from a tangle
  root (the `<<*>>` root, or any `file=` file-root) is reported as a
  non-fatal warning on stderr (`chunk <<name>> is defined but never
  used`). Reachability-based, so an entire dead subtree surfaces — it
  most often means a typo in a `<<reference>>`. Compilation still
  proceeds.

## Errors specific to tangling

- **No root chunk** — the document defines no `<<*>>=` block.
- **Undefined chunk reference** — `<<NAME>>` references a chunk that is
  never defined; reported at the reference site.
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

- **Chunk definitions are shared** across the whole document; each
  file-root pulls in whichever chunks it references. So you describe the
  architecture as one narrative and partition it into modules with
  `file=` blocks.
- The generated modules **`import` each other normally** (`import
  "./mathx"`), resolved against the document's directory. The whole set
  is tangled in memory, so nothing is written to disk for a plain
  compile / `-interp` / `-check`.
- **Same `file=PATH` repeated** concatenates in document order, exactly
  like same-name chunk pieces.
- The **compile entry** is the file marked `entry` (or, if exactly one
  file is defined, that one; or the sole module defining a `main`
  function). Mark one block `file=app.fern entry` to be explicit.
- **`fern -tangle`** prints every module under a `// ==> path <==`
  banner; **`fern -weave`** labels each file-root `📄 path`.
- **Diagnostics still map back to the document**: each module carries
  its own provenance map, so a type error in any generated file points
  at the `.fern.md` line you wrote.

A document either uses `file=` blocks (multi-module) or the single
`<<*>>` root (one module) — `<<*>>` is the shorthand for the common
one-file case.

## Example

See [`examples/literate/fizzbuzz.fern.md`](../examples/literate/fizzbuzz.fern.md)
for a complete single-file program that defines its chunks out of order,
references a chunk inside an indented `while` body, and reads as prose
top to bottom; and
[`examples/literate/multi_module.fern.md`](../examples/literate/multi_module.fern.md)
for a two-module program assembled with `file=` roots.

## A literate document as an importable library

A `.fern` file (or another `.fern.md`) can `import` a literate document
as an ordinary module — write `import "./greet"` and, when no plain
`greet.fern` exists, a sibling `greet.fern.md` is tangled in memory and
loaded in its place:

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

## Scope and current limitations

- The chunk grammar uses the `<<name>>` delimiters from `noweb`/WEB.
  A whole-line literal `<<name>>` is written `\<<name>>` (see Escaping
  above); `<<` / `>>` elsewhere on a line are never treated as markers.

## Implementation map

The engine is `internal/literate`: `Parse` → `Document`; `(*Document).Tangle`
expands the root chunk `<<*>>` and returns generated Fern source plus a per-line
provenance map; `(*Document).Weave` renders the cross-referenced Markdown.

Multi-module documents live in `internal/literate/tanglefiles.go`: `TangleFiles`
returns one `FileResult{Path, Code, LineMap, IsEntry}` per output path, and
`EntryFile` resolves the compile entry. `expandBody` / `expandChunk` are the
shared recursion behind both `Tangle` (root chunk) and `TangleFiles` (file-root
bodies).

On the CLI side (`cmd/fern/main.go`): `loadEntry` tangles a `.fern.md` entry in
memory before the normal compile / `-check` / `-interp` pipeline, and
`loadMultiFileEntry` feeds every generated module to `modload.LoadWith` as
virtual-file overrides (keyed by path relative to the document dir), loading from
the entry.

Importing a literate library goes through `modload.readModuleSource`, which falls
back to a sibling `.fern.md` when a `.fern` import target is missing and tangles
it; `LoadWithLiterate` returns the per-module `LiterateModule{DocPath, DocSrc,
LineMap}` provenance, so the CLI's `entry.remaps` (a per-module
`litRemap{docPath, docSrc, remap}`) maps an imported library's diagnostics onto
*its* document. Diagnostics are rendered through `diag.FormatRemapped`, the
literate-only sibling of `diag.Format`.

## Maintaining this

Coverage lives in `internal/literate/*_test.go` (including
`tanglefiles_test.go`), the `diag` `FormatRemapped` tests, and
`internal/e2e/literate_test.go` + `literate_multifile_test.go` (interp + tangle
+ weave, plus the single- and multi-file diagnostic-remap contracts). Examples:
`examples/literate/fizzbuzz.fern.md` (single root) and `multi_module.fern.md`
(multi-file).

When extending the chunk grammar or the remap, add cases at the layer you
touched. **The diagnostic remap — generated line → document line — is the most
regression-prone surface here**, so it is the one to cover first.
