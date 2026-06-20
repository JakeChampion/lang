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

## Follow-ups

- `Rect { w: …, h: … }` **construction** syntax.
- Named-field patterns in `if let` / `let … else` (match-only for now).
- Self-host compiler support (the self-host parser doesn't yet parse the
  named forms; no stdlib uses them, so the bootstrap is unaffected).
