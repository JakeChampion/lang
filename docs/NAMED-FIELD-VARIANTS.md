# Named-field enum variants

Status: [reference] shipped.

Enum variants may carry **named fields** instead of positional payloads:

```fern
enum Shape {
    Circle { r: f64 },
    Rect { w: f64, h: f64 },
    Unit,                       // payloadless, as before
    Pair(f64, f64),             // positional, as before
}
```

A variant is one of three forms:

- **payloadless** — `Unit`
- **positional** — `Pair(f64, f64)` (payloads accessed by position)
- **named-field** — `Rect { w: f64, h: f64 }` (payloads accessed by name)

## Matching

Named-field variants are destructured with `{ }`, using the same field list
a struct pattern takes (`StructPattern` in `spec/grammar.ebnf` covers both).
Field order in the pattern is free, and each field is one of:

- `f` — binds field `f` to a local of the same name;
- `f: local` — binds field `f` to `local`;
- `f: <pattern>` — matches field `f` against a sub-pattern (a literal, or a
  variant pattern such as `Some(t)`), binding whatever that pattern binds.

Every field must be named, unless the pattern ends in `..`, which leaves the
rest out. A struct pattern keeps the same rule, with the same E015:

```fern
match (s) {
    Circle { r } => 3.14 * r * r,
    Rect { w: 0.0, .. } => 0.0,             // sub-pattern; `h` left out
    Rect { h: height, w } => w * height,   // order-free; `h` renamed
    Unit => 0.0,
    Pair(a, b) => a + b,                    // positional variants still match with ()
}
```

The same forms work wherever a variant pattern does: in `if let`, and nested
in a payload slot (`Some(Rect { w: ww, .. })`). A field `..` leaves out
matches anything, so an unguarded arm whose named fields are all binders
covers its variant for exhaustiveness; a field sub-pattern makes the arm
refutable, exactly as a positional payload sub-pattern does.

Errors (E015): naming a field the variant does not have, naming a field
twice, leaving a field out without `..`, and using `{ }` on a positional
variant.

## Construction

A named-field variant is built by name, with the record literal form:

```fern
let r = Rect { w: 3.0, h: 4.0 };   // r has the type Shape
let s = Rect { h: 4.0, w: 3.0 };   // the same value
```

The fields can be in any order. The literal evaluates its field values in
written order, as a record literal does (`docs/EVALUATION-ORDER.md`). The
variant keeps the values in declared order.

The type of the literal is its enum. A variant is not a type of its own, so
`r.w` is an error. Read a field with a pattern.

A module-qualified literal names a variant that an imported module exports:
`shapes.Rect { w: 3.0, h: 4.0 }`. An enum-qualified literal names the enum:
`Shape.Rect { w: 3.0, h: 4.0 }`.

Two enums can declare a variant of the same name. Then a bare literal
builds the variant of the enum that its position expects, as a bare
variant does. Where no position expects one of the enums, the literal is
an E036 error. Name the enum to correct it.

Errors:

- E043: a field that the variant does not have.
- E005: a field that the literal leaves out.
- E007: a field that the literal names twice.
- E036: a field value whose type is not the type of the field.
- E043: a literal of a positional or a unit variant. Build `Circle(1.0)` and
  `Empty` without braces.
- E043: a literal with a base (`Rect { ...r, w: 1.0 }`).
- E036: a bare literal of a variant that two enums declare, where no
  position expects one of them.

The positional form `Rect(3.0, 4.0)` is also accepted, with the arguments in
declaration order. #14397 removes it. Do not use it in new code.

## Derives

`@derive(Display)`, `@derive(Debug)`, and `@derive(Json)` render
named-field variants with their field names:

| Derive | Output for `Rect { w: 3, h: 4 }` |
| --- | --- |
| `Display` | `Rect { w: 3, h: 4 }` |
| `Debug` | `Rect { w: 3, h: 4 }` (payloads via `Debug`) |
| `Json` | `{"Rect":{"w":3,"h":4}}` (object, vs. `[…]` for positional) |

`Eq` / `Ord` / `Hash` / `Default` compare/produce payloads positionally
and are unaffected by names.

## Implementation

The runtime layout is **identical** to a positional variant: payloads are
laid out in declaration order in the tagged union, so field names are
purely a parse/check concern and the IR / codegen / interpreter are
unchanged.

- `ast.EnumVariant.FieldNames` (parallel to `Payloads`; empty = positional).
- Parser: `parseEnumDecl` accepts `V { f: T, … }`. A `V { … }` pattern is
  parsed by `parseNamedFieldPattern`, the same function a struct pattern
  uses, setting `MatchArm.NamedFields`, `FieldNames` and `RestWritten`.
- Checker: `settleNamedVariantPattern` validates the pattern against the
  variant's `FieldNames` and rewrites its bindings, field sub-patterns and
  field names into declaration order, with `_` at a field `..` left out, so
  every later stage — exhaustiveness, the interpreter — treats it
  positionally. It serves a top-level arm of either match form and a
  variant sub-pattern nested in a payload slot.
  `synthEnumDisplay` / `synthEnumDebug` / `synthEnumJson` emit the named
  shape.
- Printer: `fern -fmt` round-trips the named decl + pattern forms.

### Self-host (#6676)

Same layout, same "names are a front-end concern" split:

- `parser.StructDecl.variant_field_names` — parallel to the variant record's
  fields, which keep the positional `__ev` / `__ev1` marker names, so nothing
  downstream of the parser changes.
- `parser.PatVariant.field_names` / `rest_written` — the fields a `V { … }`
  pattern named and its trailing `..`. `parse_record_variant_pattern` reads
  the field list with `parse_struct_arm_pattern`, the struct pattern's own
  parser, and encodes each field as the payload slot a positional pattern
  would carry: a binder, a literal slot tested through `lit_cond`, or a
  sub-pattern slot in `nested`. `resolve_variant_fields_module` settles the
  pattern against the declaration: slots move into declaration order, `_`
  fills a field `..` left out, and the record markers are cleared. It runs
  at the end of `parse_module` and again on the bundle (where a variant from
  an import has a declaration to settle against), over plain matches and the
  arms a desugared match keeps for exhaustiveness; a settled pattern is
  positional, so the second run is a no-op.
- A pattern that does not settle keeps its field names and the checker's
  `record_pattern_diags` reports it — the same E015 set the Go checker
  reports. The fields it did name correctly still bind, so the arm body does
  not cascade into undefined-name errors.
- `V { … }` at an arm is also how a struct pattern is spelled. The self-host
  parser decides on the head name: a struct declared in the same file takes the
  struct-pattern desugar, anything else (a record variant, or a name from an
  import) takes the variant path. `Par.structnames` is pre-scanned from the
  token stream so the decision does not depend on decl order.

`fern -fmt` reconstructs the record DECL form; a settled record PATTERN prints
as the positional pattern it was rewritten to, which re-parses to the same
program. A pattern on a variant declared in another module is not settled when
the file is formatted, and prints as written.

## Follow-ups

- Named-field patterns in `let … else` — `match` and `if let` take them, but
  `let Rect { w, h } = e else { … }` is a parse error on native, so the
  self-host leaves it one too rather than growing surface native lacks.
- Self-host `@derive(Display/Debug/Json)` renders a record variant's payloads
  positionally, where native renders the named shape.
