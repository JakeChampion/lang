---
title: Standard library
description: Reference for all 77 modules in Fern's standard library, generated from the source they document.
sidebar:
  order: 0
---

**77 modules, about 42,000 lines of Fern.** One page each, listing every
public function, struct, enum and constant with its doc comment. The pages
are generated from the Fern source they document and rebuilt with the site,
so they cannot drift from the code — to fix a description, edit the doc
comment above the declaration ([how](../contributing/)).

There is no prelude. A program sees only what it `import`s, with three
exceptions that are part of the language rather than a library: `Option`,
`Result` and `IoError`.

## What is in there

Browse the sidebar for the full list. The highlights, if you are deciding
whether the language can carry your program:

- **Text** — `string` is the largest module in the library, alongside
  `unicode`, `utf8`, `format`, `textwrap`, `table` and `ansi`. Pattern
  matching comes in two flavours: `regex` is a Thompson-NFA engine that
  matches over a *set* of positions, so alternation and star cannot blow
  up, and `peg` is an ordered-choice parser for when a grammar is the
  honest description.
- **Collections** — `array` and `map` for the everyday case, plus a
  persistent set: `pvec` (a 32-way bit-partitioned trie), `pmap` and
  `pset` (Bagwell HAMTs), and the ordered `ordmap` / `ordset`.
- **Data** — `json`, `csv`, `url`, `base64`, `base32`, `hex`, `uuid`,
  `semver`, and `crypto`: MD5, the SHA-1 and SHA-2 families, BLAKE2b,
  HMAC, PBKDF2, HKDF and TOTP, all in pure Fern with no dependency.
- **Numbers** — `i32`, `i64`, `u32`, `u64`, `float`, `math`, `rand`,
  arbitrary-precision `bigint`, and `num`, which is where the arithmetic
  operator traits live.
- **I/O and the network** — `io`, `io_buffered`, `path`, `time`, `log`,
  `signal`, `dotenv`, `tcp`, `http`, `headers`, `fetch`, and `async` for
  overlapping requests.
- **Testing** — `test` (the TAP-13 runner, and the second-largest module
  here), `fuzz`, `sim` for deterministic simulation, and `mock_platform`.
- **Command lines** — `cli` parses `--opt V`, `--opt=V`, `-o V`, bundled
  short flags, positionals and `--`, and generates `--help` from the same
  description.
- **WebAssembly** — the `wasm_*` family is a WebAssembly binary encoder
  written in Fern. It exists so the self-hosted compiler can emit wasm
  without shelling out, rather than as an application API.

Two namespaces: `std/…` is what your code reaches for, and `core/…` holds
the primitives `std` is built on. Source lives under
[`internal/stdlib/`][1] on GitHub.

[1]: https://github.com/JakeChampion/lang/tree/main/internal/stdlib
