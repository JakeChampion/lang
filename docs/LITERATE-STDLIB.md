# Literate standard library sources

Status: [reference] shipped.

Most standard library modules are literate Fern documents (`.fern.md`).
These documents contain handwritten code, STE prose, and named chunks.
Some documents also contain generated regions. Each generated region stays inside one complete chunk with its boundary markers intact.

Many documents contain executable examples in `fern test` fences.
The root chunk preserves the original declaration order. Module imports retain their public names.
The compiler expands each source document in memory and maps diagnostics back to its document lines.

## Source edits and checks

Edit module prose in its Markdown document. Edit code inside its named chunks.
Keep the root references in declaration order. Keep generator boundary markers inside one chunk.
Declaration comments supply API descriptions and editor hover text.

Use these commands for one document:

```sh
fern -fmt -w internal/stdlib/std/hex.fern.md
fern -doctest internal/stdlib/std/hex.fern.md
fern -tangle internal/stdlib/std/hex.fern.md
fern -weave -html -o build/hex.html internal/stdlib/std/hex.fern.md
```

Run `make check-sources` for standalone import checks.
Run `make digest-check log-table-check huffman-table-check` after edits to their generated regions.
The float-table and time-zone generator tests also check their committed source documents.

The Go formatter corpus expands source documents into temporary plain modules.
This keeps its AST, format stability, and comment checks active for every literate module.
The standard-library doctest test discovers all documents with test fences.

## Plain sources

Five modules have generators that own the complete file. They keep plain Fern sources:

| Module | Generator |
| --- | --- |
| `std/brotli/dictionary` | `cmd/brotlidictgen` |
| `std/encoding/indexes` | `cmd/encodinggen/gen.py` |
| `std/publicsuffix` | `cmd/pslgen` |
| `std/tls/mozilla` | `cmd/rootsgen` |
| `std/unicode` | `cmd/unicodegen` |

Some handwritten modules are also plain sources. Examples are the submodules of `std/android` and `std/markdown`.
`core/_test_empty` and `std/_test_empty` are plain test helpers.

## Finding a module document

The source of a module is at its import path under `internal/stdlib`, with the extension `.fern.md` or `.fern`.
For example, `std/tls/pem` is `internal/stdlib/std/tls/pem.fern.md`.
A plain `.fern` source takes precedence when both formats exist.

The [generated standard library reference](https://fern-lang.netlify.app/stdlib/) lists each published module.
`fern -doc` builds the same reference from a checkout.
To list the documents in a checkout:

```sh
find internal/stdlib -name '*.fern.md' | sort
```
