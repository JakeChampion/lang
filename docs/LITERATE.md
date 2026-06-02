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
import "core/no_prelude";
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

## The CLI

```sh
fern -tangle prog.fern.md     # write the tangled Fern source to stdout
fern -weave  prog.fern.md     # write a cross-referenced Markdown reading copy
```

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
code chunks rather than a full CommonMark engine.

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
  Literal `<<` / `>>` in code aren't currently part of Fern's syntax,
  so there is no escaping mechanism; if that changes, references will
  need an escape.
