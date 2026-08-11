# Named-field enum variants

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

Named-field variants are destructured with `{ }`, binding each field to a
local of the same name (Rust's field-shorthand). Field order in the
pattern is free, and **all** fields must be bound:

```fern
match (s) {
    Circle { r } => 3.14 * r * r,
    Rect { h, w } => w * h,       // order-independent; both fields required
    Unit => 0.0,
    Pair(a, b) => a + b,          // positional variants still match with ()
}
```

Errors: binding an unknown field, omitting a field, or using `{ }` on a
positional variant (or `( )` on a named-field variant) are all checker
errors (E015).

## Construction (v1)

In this first cut, **named-field variants are constructed positionally**,
arguments in declaration order:

```fern
var r: Shape = Rect(3.0, 4.0);   // w = 3.0, h = 4.0
```

The struct-literal construction form `Rect { w: 3.0, h: 4.0 }` is a
planned follow-up (it needs a StructLit→variant node rewrite the checker
doesn't do yet).

## Derives

`@derive(Display)`, `@derive(Debug)`, and `@derive(Json)` render
named-field variants with their field names:

| Derive | Output for `Rect(3, 4)` |
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
- Parser: `parseEnumDecl` accepts `V { f: T, … }`; match arms accept
  `V { f, … }` (`parseNamedFieldPattern`), setting `MatchArm.NamedFields`.
- Checker: `resolveVariantBindings` validates the named pattern against
  the variant's `FieldNames` and reorders the bindings + types into
  declaration order, so every later stage treats them positionally.
  `synthEnumDisplay` / `synthEnumDebug` / `synthEnumJson` emit the named
  shape.
- Printer: `fern -fmt` round-trips the named decl + pattern forms.

### Self-host (#6676)

Same layout, same "names are a front-end concern" split:

- `parser.StructDecl.variant_field_names` — parallel to the variant record's
  fields, which keep the positional `__ev` / `__ev1` marker names, so nothing
  downstream of the parser changes.
- `parser.PatVariant.field_names` — the fields a `V { f, … }` arm named, with
  the bindings holding the locals they were bound to.
  `resolve_variant_fields_module` settles the pattern against the declaration:
  bindings become the fields in declaration order and `field_names` is cleared.
  It runs at the end of `parse_module` and again on the bundle (where a variant
  from an import has a declaration to settle against); a settled pattern carries
  no field names, so the second run is a no-op.
- A pattern that does not settle keeps its field names and the checker's
  `record_pattern_diags` reports it — the same E015 set native reports,
  including the renaming (`f: local`) rejection.
- `V { … }` at an arm is also how a struct pattern is spelled. The self-host
  parser decides on the head name: a struct declared in the same file takes the
  struct-pattern desugar, anything else (a record variant, or a name from an
  import) takes the variant path. `Par.structnames` is pre-scanned from the
  token stream so the decision does not depend on decl order.

`fern -fmt` reconstructs the record DECL form; a settled record PATTERN prints
as the positional pattern it was rewritten to, which re-parses to the same
program.

## Follow-ups

- `Rect { w: …, h: … }` **construction** syntax.
- Named-field patterns in `let … else` — `match` and `if let` take them, but
  `let Rect { w, h } = e else { … }` is a parse error on native, so the
  self-host leaves it one too rather than growing surface native lacks.
- Self-host `@derive(Display/Debug/Json)` renders a record variant's payloads
  positionally, where native renders the named shape.
