# Associated types

A trait may declare **associated types** — a type member each implementer
fixes — and refer to them in method signatures via a projection:

```fern
trait Iterator {
    type Item;
    function next(self: Self): Option[Self::Item];
}

struct IntRange { lo: i32, hi: i32 }
impl Iterator for IntRange {
    type Item = i32;
    function next(self: Self): Option[Self::Item] { ... }
}
```

A projection is written `Base::Name`:

- **`Self::Item`** — inside a trait or impl, the implementing type's binding.
- **`T::Item`** — in a bounded generic, the binding of whatever `T` is
  instantiated with.
- **`Foo::Item`** — a concrete type's binding.

This is the feature that lets generic code abstract over the associated
type without an extra type parameter at every use:

```fern
function first[I: Iterator](it: I): I::Item {
    return it.next();          // I::Item resolves per instantiation
}
```

## Semantics

- An `impl` must bind **exactly** the trait's associated types — every one,
  and no extras (errors: *must bind associated type "Item"* / *binds
  associated type "Extra" which the trait does not declare*).
- A projection resolves to its binding: `Foo::Item` immediately (the impl
  is known); `Self::Item` / `T::Item` once the base becomes concrete — at
  impl conformance, at a generic call site, or when the generic is
  monomorphised.
- **Object safety**: a trait with associated types is **not** usable as
  `dyn Trait` (a `dyn` value erases the concrete type, so the binding can't
  be recovered). Rust's `dyn Trait<Item = T>` pinning is a follow-up.

## Implementation

The runtime is unaffected — a projection is always resolved to a concrete
type before codegen, so the IR / backends / interpreter never see one.

- **lexer**: `::` token.
- **ast**: `ProjType{Base, Name}`; `TraitDecl.AssocTypes`;
  `ImplDecl.AssocTypeBindings`. `SubstSelf` / `Equal` / `String` handle
  `ProjType`.
- **parser**: `type Item;` in traits, `type Item = T;` in impls, and
  `Base::Name` in any type position.
- **checker**: the conformance pass validates + records each impl's
  bindings (`Info.AssocBindings`) and compares method signatures with
  projections resolved on both sides (`resolveProjWith`);
  `resolveProjections` (run each `Check`, after conformance) rewrites every
  concrete-base projection in signatures + bodies to its binding;
  generic call results resolve the projection after type-argument
  substitution; `objectSafe` rejects `dyn` for associated-type traits.
- **monomorph**: `substituteType` carries `ProjType` (substituting the
  base); the checker re-check then resolves the now-concrete projection.

## Scope / follow-ups

- **`dyn Trait<Item = T>`** pinning (associated-type traits as trait objects).
- **Associated-type bounds** (`type Item: Display;`).
- **Associated-type defaults** (`type Item = i32;` in the trait).
- **Self-host compiler** support (the self-host parser doesn't yet parse
  `::` projections or `type` members; no stdlib uses associated types, so
  the self-host bootstrap is unaffected).
