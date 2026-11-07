# Fern coherence review (November 2026)

Status: [record] review snapshot at commit 8b5850dc (2026-11-07).

This review asks one question: does Fern make sense as one whole? It
examines the language, the `fern` command line and the standard library
together. Each claim below was run on `bin/fern` built from HEAD, and on
the Go oracle where the two engines can differ. Each defect has an issue.

The 2026-07 review (`LANGUAGE-REVIEW-2026-07.md`) is the previous record.
Its "two mutation semantics" finding is closed: E048, E055 and E056 refuse
a field write, a discarded update and a subscript write in both checkers.
Its "mixed error conventions" finding is mostly closed: all `index_of` and
`find` functions return `Option`, and all 61 stdlib error types implement
`core/error.Error`.

## Summary

The core is coherent. Data is immutable, but code can assign a new value
to a local. `Option` and `Result` with `?` carry absence and failure.
`match` is exhaustive, and integer semantics are portable.

The defects are at the seams. In six places a newer mechanism replaced
part of an older one, and both remain:

| Area | The two mechanisms | Issue |
| --- | --- | --- |
| Sum types | `enum` and `type A \| B` unions | #14397 |
| Methods | receiver functions and `impl` methods named by `self` | #14401 |
| Array operations | methods, free functions and `\|>`, each a different subset | #14398 |
| Scope | "no prelude" and about 190 builtins with 27 reserved type names | #14400 |
| Effects | the `Platform` trait and ambient host builtins | #14400 |
| Command line | modes written as flags, with no conflict check | #14399 |

Also, the engines do not always agree. The self-host interpreter, native
code and the Go oracle gave three different results across the probes
below.

## Defects

| Issue | Defect |
| --- | --- |
| #14388 | An integer literal gets its type from a later statement. A `let` after a `print` changed the printed value from `-294967296` to `4000000000`. The oracle prints `-294967296`. |
| #14389 | `==` on a struct with a user `eq` method is structural on the self-host interpreter. Native code and the oracle call `eq`. |
| #14390 | `p as Big`, where `p` is a `usize`, reinterprets a pointer in safe code. |
| #14391 | `string_from_bytes_unchecked` and `slice_unchecked` make a `string` that is not valid UTF-8, outside `unsafe`. |
| #14392 | A receiver method without `pub` is callable from another module. |
| #14393 | `a..b` becomes `iter.range(a, b)` in the user's scope. A local named `iter` changes the value of `0..3`. |
| #14394 | A `let` initializer mismatch is E003. `-explain E003` says that this case is E002. |
| #14395 | `"01.2.3.4".is_ipv4()` is `true`. `net.is_ipv4("01.2.3.4")` is `false`. |
| #14396 | E043 on an array lists only the four builtin methods. It does not name the imported free function of the same name. |

## Language

**Sum types (#14397).** A named-field enum variant is built by position,
`Rect(3, 4)`, and matched by name, `Rect { w, h }`. A struct in a union is
built by name and matched by position, `A(a)`, which binds the whole value.
So the pattern `X(p)` has two meanings. The compiler uses 45 unions. The
stdlib uses none.

**Block tails (#14401).** A value block and a lambda body give their last
expression as the value. A function body does not. `function f(): i32
{ x + 1 }` is E052, and `function f(): void { 5 }` drops the `5` with no
diagnostic.

**Statement heads (#14401).** `if`, `while` and `match` need parentheses.
`for … in` and `if let` do not, and they use a second precedence ladder in
`spec/grammar.ebnf` for the same `{` ambiguity.

**Method spellings (#14401).** An inherent method has a receiver. An `impl`
method must name its first parameter `self`. The self-host uses
the name to decide that a function is a method.

**Names from the TypeScript start (#14401).** `boolean` and `void` remain.
`()` is the unit value, but the unit type is `void`. The three-part `for`
and `use x <- e` duplicate other forms. Lambda parameters must carry types,
because `=>` is also the function-type arrow and the match-arm arrow.

**Bindings.** No local binding is immutable. There is no local `const`, and
code can assign to a parameter and to a range-`for` counter.

**Ownership.** The stdlib has 715 `own` parameters. A caller that passes a
local and reads it later gets E051. No explicit copy is available.

## Command line

The command line is in #14399.

- The default target is `arm64-linux` on every host. On x86-64,
  `fern -o hello x.fern` makes a binary that the host cannot run.
- When two modes are given, one of them is done and the other is ignored
  with no diagnostic. `-check -fmt` formats and does not check.
- A sub-flag without its mode falls through to a full compile.

The output contract and the names also differ between modes:

- `-lint` writes its findings to stdout. Other modes write diagnostics to
  stderr. `-q` does not change `-lint`. No JSON format exists outside
  `-lsp`.
- `-profile` is a flag preset. `FERN_PROFILE` selects profile
  instrumentation. About 27 debug variables share the `FERN_` prefix with
  the user variables.

The flag parser is a strength. One table drives both the parser and `-h`,
so the help text cannot list a flag that does not exist.

## Standard library

**One call surface (#14398).** `std/array` has 45 operations in two forms,
20 as free functions only and 41 as methods only. `|>` reaches only the
free functions. Type-suffixed families remain where a trait exists:
`json.get_i32`, `i32.parse_i32`, and about 22 functions in `std/test`.

**Scope and effects (#14400).** `Platform` makes the simulation and mock
modules work, but only the HTTP middleware takes it. `clock`, `rand`,
`log`, `dns`, `net` and `io` call the host directly. `core/` mixes public
traits with internal kernels, so it is not a tier that users can see.

**Placement (#14402).** The `u8` methods are in `std/i32`, the `f32`
methods are in `std/f64`, and the `usize` methods are in `std/u64`.
`std/test` imports `std/http`, so the test runner reaches 13 host
capabilities. `json.Document.first`, `path.extension`, `string.before` and
`cli.parse` report absence or failure in band.

The strengths: the persistent collections share one vocabulary, the error
types are uniform, and `time` (pure) and `clock` (hosted) divide by host
need.

## Documentation corrected with this record

The site had these errors:

- The tutorial said that `float` is an alias for `f64`. The parser refuses
  `float`.
- The tutorial and the reference said that `.to_string()` on an `i32` needs
  `import "std/i32"`, and that a program has no prelude.
- The reference said that an f-string hole needs an import for a string or
  a number. It also said that only a character literal takes `\u{…}`, and
  it left `~` out of the precedence table.

The repository docs had these errors:

- README and the tooling reference said that the freestanding targets have
  no emitter. They emit assembly. `arm64-kernel` was in neither table.
- `CLOSURE-CAPTURE.md` used the retired `function ()` lambda form.
- `FREESTANDING-CORE.md` gave the old partition count and the old `std/test`
  reach.
- `STDLIB.md` put big integers in `core/`.
- `IDIOMS.md` said that a variant-typed local cannot take another variant.
  That is true only for a union member.
