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
- **Object safety**: a trait with associated types is usable as a trait
  object only when the `dyn` type PINS every one —
  `dyn Holder[Item = i32]`. A `dyn` value erases the concrete type, so an
  unpinned associated type has nothing to resolve against at the call site
  and bare `dyn Holder` is E021.

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

## The self-host compiler

The self-host implements the feature too, in a different shape because its
declared types are SPELLINGS rather than a tree:

- `::` is its own token. In expression position it is a path separator
  equivalent to `.` (`at_path_sep` accepts either); in TYPE position the two
  are not interchangeable — `mod.Type` is a module qualifier and `Base::Name`
  a projection — so the lexer's old fold of `::` into `.` left them
  indistinguishable.
- A projection is the string `Base::Name`, and
  `parser.resolve_assoc_projections` rewrites every one to the impl's binding
  as a module pass. The bindings are syntactic (`type Item = i32;` on the
  impl), so unlike native this needs no inference and runs in the parser.
- The conformance comparison resolves BOTH sides: the impl method's copy is
  already rewritten, so the trait's requirement must be too, or every
  associated-type impl reads as a signature mismatch.

`TestSelfHostAssocTypesDifferential` holds conformance, resolution and object
safety to native's exact diagnostics.

One divergence is deliberate and is NOT this feature's: where a projection has
no binding, native also reports a cascading `E002 return type mismatch` against
the unresolved type and the self-host does not. It declines that E002 for any
unknown nominal return (`function f(): Nope { return 1; }`) — its documented
zero-false-positive conservatism, and a separate convergence item.

## Scope / follow-ups

- **Associated-type bounds** (`type Item: Display;`).
- **Associated-type defaults** (`type Item = i32;` in the trait).
