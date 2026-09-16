# 2026-09-16 — a destructure projects at every level

`unsupported destructuring declaration` sat at 25 corpus sites after the
type-headed paths landed: every nested position (`let (a, (b, c)) = t`),
every struct pattern (`var P { x: N, y: b } = p`, and the prelude a
destructuring PARAMETER lowers to), and every `@` binder.

## What it was

`declare_tuple` produced one flat level of one tuple: the names were the
top-level comma-separated list, a name in parentheses or any marker on
the statement (`@sd:`, `@at:`, `@rest;`, `@pp;`) returned no names, and
the declaration was refused. The AST lowering has handled all of these
since #6165-era work, one temp per level, so nothing in the language was
missing — only this boundary's reading of the statement.

## What changed

`declare_tuple` produces the initializer once and then projects: a tuple
position is a `tuple_get`, a nested position is projected and
destructured again from that projection, a struct pattern's names are
`record_get` projections of the fields the `@sd:` marker lists, and the
`@` binder is bound to the whole value. Every binding's type is the
checker's post-declaration type and must equal the projection's; a
discard is a binding no read follows. There is no per-level temp: the
inner tuple is a value like any other, and the unit planner keeps the
whole live under the borrows as it does for any container.

Corpus, whole modules produced: 378 → 382 (the remaining destructuring
sites refuse for other reasons: a void call in expression position, a
loop binding).

## The `for` header

A `for (k, w) in xs` header takes the same pattern, and `iterate_array`
refused it as an "unsupported loop binding" (7 sites). The element is now
destructured by the same projection walk, in the body's checker scope,
before the body is produced. A Map iterand still refuses: there is no
entry projection to walk.

## Pinned

`TestSelfHostSemanticSourceRC` runs `struct_unpack` (a struct pattern
with a renamed field and a rest), `at_unpack` (an `@` binder on a struct
pattern whose fields hold a string and an array) and `nested_unpack` (a
nested position holding a fresh string and an array) on arm64, x86-64,
the sanitiser and wasm under the leak check. The print golden carries
`nested_destructure`, which had pinned the refusal, and
`struct_destructure`. `for_pairs` runs a flat and a nested header over
arrays of tuples holding fresh strings; the print golden carries
`for_pair`.
