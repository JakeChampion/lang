# A VCL front end, in Fern

A lexer, parser, syntax tree, and printer for the **Varnish Configuration
Language** (VCL 4.0 / 4.1) — the language Varnish Cache users write to
express caching policy. It is written in Fern, depends on nothing outside
`std`, and parses real-world VCL.

```
$ fern -interp vclfmt.fern -- testdata/sample.vcl     # reformat
$ fern -interp vcl_test.fern                          # the TAP suite
$ fern -target x86-64-linux -o vclfmt vclfmt.fern     # a static binary
$ ./vclfmt broken.vcl; echo $?
broken.vcl:3:7: expected a variable name after 'set', found '='
1
```

`vclfmt` exits 0 when the file parses clean, 1 when it does not, and 2
when it cannot be read — so it works as a syntax check in a pipeline, not
only as a formatter.

## Layout

| File | What it is |
|---|---|
| `ast.fern` | The tree. One module, because the node types are mutually recursive. |
| `lexer.fern` | Tokenizer: identifiers, the three string forms, unit suffixes, comments, inline C. |
| `parser.fern` | Recursive descent + precedence climbing, with per-statement error recovery. |
| `printer.fern` | Tree back to source. |
| `vclfmt.fern` | The driver. |
| `vcl_test.fern` | 38 TAP cases. |
| `diag.fern` | A source-located diagnostic. |

## What it covers

`vcl 4.1;`, `import` (both forms), `include`, `backend` (including the
4.1 `backend name none;`), `probe`, inline probes as block-valued
properties, `acl` with all four entry forms, and `sub`.

Inside a subroutine: `set` with all five assignment operators, `unset`,
`if`/`elseif`/`elsif`/`else if`/`else`, every `return` form including
`return (synth(750, "Moved"))`, `call`, `new`, bare call statements,
`include`, and `C{ … }C`.

Expressions: the full operator set with VCL's precedence, `~` and `!~`
against both regexes and ACL names, string concatenation with `+`,
duration (`ms`/`s`/`m`/`h`/`d`/`w`/`y`) and byte-size
(`B`/`KB`/`MB`/`GB`/`TB`) literals, and all three string forms —
`"short"`, `{"brace"}`, and `"""triple"""`.

The tree is deliberately **concrete**: it keeps written parentheses, the
`elseif` spelling each arm used, and which delimiters a string was
written with. That is what makes `parse` → `print` a fixed point, which
is the property the test suite and the Go gate both check.

## What it does not do

**There is no checker and no back end.** Those are the two halves that
would make this a compiler rather than a front end:

- A **checker** would enforce VCL's real rules: which variables are
  readable and writable in which subroutine (`bereq.*` only in backend
  subs, `resp.*` not in `vcl_recv`), which `return` actions each
  subroutine may use, and the type and coercion rules over
  `STRING`/`BACKEND`/`IP`/`TIME`/`DURATION`/`BOOL`/`INT`/`REAL`/`ACL`.
  That is table-driven work over the tree this module already produces —
  the same shape as `internal/platforms` in this repo.
- A **back end** has three plausible targets: an interpreter plus a cache
  runtime (self-contained, but it means reimplementing varnishd); C
  emitted against the VRT ABI for real `varnishd` to `dlopen` (what
  Varnish's own VCC does, and an ABI-tracking commitment); or emitting
  Fern source and letting `fern` produce the binary.

Two known gaps a checker or evaluator would hit, worth knowing before
building on this:

- **`std/regex` is not PCRE2.** It is a Thompson NFA — no backreferences,
  no lookaround, and `(?i)` only as a leading flag. Varnish's `~` is
  PCRE2, and real VCL uses the difference. Anything that *evaluates* a
  match needs a documented subset, an extended engine, or a PCRE2
  binding, which Fern has no native FFI for today.
- **VMODs are dynamically loaded shared objects.** A Fern binary is
  static, so `import std; import directors;` can only ever be built-in
  shims here, not a real `dlopen`.

An unterminated `/* … */` runs to end of input and is reported as an
unexpected end rather than as its own diagnostic; `vcc` names the comment.

## Why it lives in this repo

A parser is the densest ordinary user of the features a self-hosting
language has to get right: recursive unions with struct payloads,
exhaustive `match`, arrays of nodes grown by `append`, and — since Fern
struct fields are immutable after construction — a cursor threaded as a
return value rather than mutated in place. `internal/e2e/vcl_example_test.go`
runs the suite and the fixed-point check on every CI run, from a program
nobody is tempted to shape around a compiler bug.
