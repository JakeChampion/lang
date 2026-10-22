# Associated types

Status: [reference] trait associated types and `Base::Name` projections, as shipped.

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
- A **parametric impl** may bind to its own type parameter:

  ```fern
  impl[T] Carrier for Box[T] { type Ok = T; … }
  ```

  The binding is then read through the base's type arguments, so
  `Box[i32]::Ok` is `i32`. The parameter is recovered by unifying the impl's
  `for` pattern against the base, which is why a binding may be a composite
  of the parameter (`type Item = Option[T];`) or name any parameter the impl
  declares, in any order.
- A projection resolves in a **parameter** as well as in the result, once an
  earlier argument has pinned its base:

  ```fern
  function pick[H: Holder](h: H, d: H::Item): H::Item { … }
  pick(b, 0)            // H = Box[i32] from argument 1, so `d` is i32
  ```

  Inference is a single left-to-right pass, so the base must be pinned by an
  argument BEFORE the one whose type projects on it. A projection the call
  cannot pin stays unresolved, exactly as before — `X::Residual` alone does not
  determine X.
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
  base) and `rewriteType` flattens the base to its mangled instantiation, so
  the re-check sees the `Box__i32::Ok` the synthesised concrete impl records
  its binding under. A generic **enum** base has no such instantiation —
  `rewriteType` keeps one generic decl for it — so `rewriteType` resolves that
  projection outright (`resolveAssocBinding`) while the first check's bindings
  are still in hand, which is the moment "when the generic is monomorphised"
  names above.

A parametric impl's binding is written in the impl's own type parameters, so
two extra steps carry it: `resolveTypeNames` resolves those references to
`ParamType` (leaving them as same-named `StructType` produced the
identical-printing *returns T but expression is T*), and `Info.AssocBindingPattern`
records the impl's `for` pattern beside the binding so `substAssocBinding` can
unify it against the concrete base.

## The self-host compiler

The self-host implements the feature too, in a different shape because its
declared types are SPELLINGS rather than a tree:

- `::` is its own token and means a projection only: in type position
  `mod.Type` is a module qualifier and `Base::Name` a projection, and in
  expression position `::` is a P001.
- A projection is the string `Base::Name`, and
  `parser.resolve_assoc_projections` rewrites every one to the impl's binding
  as a module pass. The base carries its type arguments (`Box[i32]::Ok`), so
  the rewrite steps back over the bracket group to recover it, matches the
  impl on the base NAME, and substitutes the impl's parameters — recovered
  positionally from its `for` spelling, and filtered to the ones
  `ImplInfo.all_type_params` says the impl declares — through the base's
  arguments. Erasure hides a missed resolution whenever the answer does not
  depend on the type, so the gate turns on one that does (`.len()` on a
  string payload). The bindings are syntactic (`type Item = i32;` on the
  impl), so a projection on a concrete base needs no inference and resolves
  in the parser.
- A projection on a TYPE PARAMETER (`H::Item` in
  `first[H: Holder](h: H): H::Item`) has no impl until a call binds `H`.
  `subst_ty` substitutes through a projection's base, and the result is
  resolved at the three places a binding is known: the checker's typing of
  the generic call (`projected_ret`, which is what stamps the call's `c.ty`),
  the monomorphiser's call-result inference (`call_ret_type`), and the clone's
  own signature (`clone_bg`).
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

## Associated constants

A trait may also declare a **constant** each implementer fixes, read as a
projection in expression position:

```fern
trait Lanes {
    const LANES: i32;                  // every impl binds it
    const NAME: string = "lanes";      // a default an impl may omit
    function width(self: Self): i32;
}
impl Lanes for f64 {
    const LANES: i32 = 1;
    function width(self: Self): i32 { return Self::LANES; }
}
function chunk[T: Lanes](n: i32): i32 { return n / T::LANES; }
```

- `Self::LANES` inside an impl or a trait default, `T::LANES` through a bound
  and `f64::LANES` on a concrete type all read the binding. The separator is
  `::`; `.` reads the same, as it does for `T.zero()`.
- An impl binds every abstract constant with `const NAME: T = expr;`, with the
  trait's type (`Self` substituted). A `const` bound by a `function`, or a
  function by a `const`, is E021, as is a missing one (`missing associated
  constant "LANES"`) or a wrong type (the signature mismatch, as `() => T`).
- `T::f` on an associated function and `T::NAME()` on a constant are both
  E043, each naming the other spelling. A name neither declares is E043
  `T has no associated constant "X"`, on an unbounded parameter too (its
  associated functions come from its instantiation, which a read cannot
  resolve).

Implementation, both compilers: an associated constant is an associated
function with no parameters marked `const` (`TraitMethod.Const` /
`FuncDecl.AssocConst` natively; `FuncDecl.is_const` on the self-host, as a
module `const` already is), so conformance, `T.f()` dispatch through a bound,
monomorphisation and the interpreters need no new case. A read is a field
access on a TYPE name, which has no value-level fields: a scope-aware pass
(`checker.rewriteAssocConstReads` over `ast.ScopeWalker`;
`parser.rewrite_assoc_const_reads` over `astwalk`'s const-substitution walker,
on the parsed module and again on the flattened bundle) turns it into the
argument-less call marked as a read (`Call.AssocConst`;
`ExprFieldAccess.const_read`), which is how the checker tells `T::NAME` from
`T::NAME()`. An enum's field is a variant unless an impl gives the enum an
associated function of that name. The self-host interpreter binds a call's
explicit type arguments (`chunk[V2](8)`) in `bind_tyargs`, which an argument
value could not pin; `T.zero()` under an explicit instantiation reached the
same gap.

`TestSelfHostAssocConstsDifferential` holds the rules above to native's exact
diagnostics.

## Scope / follow-ups

- **Associated-type bounds** (`type Item: Display;`).
- **Associated-type defaults** (`type Item = i32;` in the trait).
