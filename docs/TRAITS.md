# Traits: static ad-hoc polymorphism for Fern

Status: **Phases 1–3 landed; `std/test` collapse landed (both
compilers).** This document is the design of record;
it describes the whole feature and is implemented in phases (see
[Phasing](#phasing)). Phase 1 (trait + impl declarations, conformance
checking, coherence), Phase 2 (bounded generics `[T: Trait]` with
deferred static dispatch), and Phase 3 (cross-module trait coherence —
qualified trait refs + modload name-rewriting) have shipped; later
phases are designed here so the early work doesn't paint us into a
corner.

### Empty impls adopt pre-existing methods

A useful property emerged from the Phase 1 conformance check: it looks
methods up in `Info.Methods` regardless of *who* registered them, so an
**impl with no method bodies** is satisfied by a type's pre-existing
methods. This is what lets a primitive that already has the method —
`i32` already carries `to_string` from `std/i32` — opt into a trait
without re-declaring (and colliding on) the method:

```fern
// i32 already has to_string from std/i32; just record conformance.
impl Display for i32 { }
```

A non-empty impl whose method would collide with an existing method is
still rejected (`method "to_string" on i32 redeclared`), so the empty
form is the intended way to adopt existing behaviour.

## 1. Motivation

Fern today has no way to abstract over types. Methods are welded 1:1 to a
concrete receiver type, and the checker rewrites every `x.m(a)` to a flat
`__method_<Type>_m(x, a)` at compile time
(`internal/checker/checker.go` dispatch path ~`:4514`). Three problems
fall out, and the codebase already complains about all three:

- **The stdlib hand-monomorphises.** `std/test` carries
  `assert_eq_i32`, `assert_eq_i64`, `assert_eq_u32`, `assert_eq_u64`,
  `assert_eq_string`, `assert_eq_f32_near`, … one per type. A comment at
  `internal/stdlib/std/test.fern:749` says verbatim: *"As soon as a
  `Display`/`ToString` trait lands, these collapse into one generic
  helper per family."* `std/format.fern:35` and `time.fern:149` echo the
  same wish.
- **Numeric methods are split by width/signedness by hand** — `i32`,
  `i64`, `u32`, `u64`, `f32`, `f64` each get their own `to_string`, with
  the receiver-hoist and the call-site dispatch kept "in lockstep" by
  duplicated `switch` ladders (`checker.go:1669` and `:4530`).
- **Method visibility is ad-hoc.** `methodVisibleHere`
  (`checker.go:1790`) gates dispatch on the module import-closure, with a
  special "stdlib is all mutually visible" carve-out to tolerate stdlib
  cycles. There is no principled rule for *where a method may be defined*.

A trait system fixes all three at once, and — crucially for Fern's
fast-startup CLI / edge-function targets — it can be **entirely
statically resolved with zero runtime cost**, because Fern already
monomorphises generics and already lowers methods to flat calls.

## 2. Prior art and the choice

| Language | Model | Coherence | Cost |
| --- | --- | --- | --- |
| **MoonBit** | nominal traits, `impl T for X` | type's *or* trait's package | **static, zero-cost** |
| Rust | nominal traits | orphan rule (trait or type local) | static + `dyn` |
| Roc | abilities + `derive` | opaque-type-anchored | static |
| Swift | protocols | nominal | witness table *or* specialization |
| Go | structural interfaces | implicit | runtime itable |
| Gleam | none (pass functions) | n/a | n/a |

We follow **MoonBit**: it shares Fern's target (Wasm), its
"fast-startup, no runtime overhead" goal, and its static-dispatch model.
Concretely we adopt: nominal traits, `impl Trait for Type`, **static
dispatch resolved by monomorphisation**, and **package/module
coherence** (the orphan rule) — which also replaces the ad-hoc
`methodVisibleHere` carve-out with a real law.

We explicitly do **not** adopt Go-style runtime structural dispatch (it
fights monomorphisation and fast startup) and we defer dynamic trait
objects (`dyn`) until a concrete heterogeneous-collection use case
demands them; Swift shows that surface can be added later without
disturbing the static path.

## 3. Surface syntax

```fern
trait Display {
    function to_string(self: Self): string;
}

trait Eq {
    function eq(self: Self, other: Self): boolean;
}

struct Point { x: i32, y: i32 }

impl Display for Point {
    function to_string(self: Self): string {
        return "(" + self.x.to_string() + ", " + self.y.to_string() + ")";
    }
}

impl Eq for Point {
    function eq(self: Self, other: Self): boolean {
        return self.x == other.x && self.y == other.y;
    }
}
```

- A **trait** is a named set of method *signatures*. Each signature's
  first parameter must be `self: Self`. `Self` is a contextual type that
  stands for the implementing type.
- An **impl** provides bodies for exactly the trait's methods, with `Self`
  bound to the `for` type. Every method must be present; signatures must
  match (modulo `Self` → `Type`); no extra methods are allowed.
- `trait` may be `pub`. Impls inherit visibility from the type/trait;
  there is no `pub impl`.

Once `impl Display for Point` exists, **`p.to_string()` works for a
`Point` value through the ordinary method-dispatch path** — an impl
method *is* a method. That is the whole of Phase 1's runtime story, and
it needs no codegen/IR/interp changes.

Bounded generics (Phase 2) let a function abstract over any `T` that
implements a trait:

```fern
pub function assert_eq[T: Display + Eq](actual: T, expected: T): Option[string] {
    if actual.eq(expected) { return None; }
    return Some("expected " + expected.to_string()
                + " but got " + actual.to_string());
}
```

## 4. Dispatch model

**Static, by monomorphisation. No vtables, no runtime lookup.**

- **Direct calls** (`p.to_string()` on a concrete `Point`) already work:
  the checker's existing dispatch rewrites them to
  `__method_Point_to_string(p)` because the impl registered that method.
- **Bounded-generic calls** (`x.to_string()` where `x: T`, `T: Display`)
  are resolved in two steps, reusing infrastructure that already exists:
  1. In the checker's first pass the receiver type is `ParamType{T}`.
     We type-check the call against the *trait* signature (so the body
     type-checks) but **leave the callee as a `FieldAccess`** — we do not
     mangle it, because the concrete type is not yet known.
  2. `monomorph.Run` clones the function per instantiation, substituting
     `T → i32` etc. in the body (it already does this for `ParamType`).
  3. `monomorph` re-runs `checker.Check` on the rewritten program (it
     already does — see the package doc at `internal/monomorph/monomorph.go:19`).
     Now the receiver is concrete and the **ordinary** dispatch path
     rewrites `x.to_string()` → `__method_i32_to_string(x)`.

So bounded dispatch costs nothing at runtime and adds no new lowering: it
is the existing monomorphise-then-recheck loop, plus a first-pass rule
that says "a method call on a trait-bound type param type-checks against
the trait and is left for the recheck."

## 5. Coherence (the orphan rule)

To guarantee a globally-unique impl for every `(Trait, Type)` pair:

> An `impl Trait for Type` is legal only if `Trait` is declared in the
> current module **or** `Type` is declared in the current module.

Plus: **at most one** `impl Trait for Type` across the whole program
(duplicate impls are an error). This is the MoonBit/Rust law. It makes
"which impl?" answerable without consulting the import graph, and it
subsumes the `methodVisibleHere` stdlib carve-out — a coherent impl is
unique regardless of who imports whom.

For single-file programs (and the entry module) every type and trait is
local, so coherence is always satisfied; the rule only bites across
modules.

## 6. Implementation

### 6.1 Lexer
Add `trait` and `impl` to `internal/lexer/lexer.go` `keywords`. `Self`
stays a *contextual* type name (recognised in the parser inside
trait/impl bodies); `self` is an ordinary identifier. Verified no `.fern`
source uses `trait`/`impl`/`Self` as identifiers (only in comments).

### 6.2 AST (`internal/ast/ast.go`)
```go
type TraitDecl struct {
    P            Position
    Name         string
    NamePos      Position
    Methods      []TraitMethod // signatures; first param is self: Self
    Public       bool
    SourceModule string
}
type TraitMethod struct {
    P      Position
    Name   string
    Params []Param   // Params[0] is self: Self (ast.SelfType{})
    Result Type
}
type ImplDecl struct {
    P            Position
    Trait        string
    TraitPos     Position
    Type         Type        // the `for` type
    Methods      []*FuncDecl // desugared to receiver-methods at parse time
    SourceModule string
}
```
Add `Traits []*TraitDecl` and `Impls []*ImplDecl` to `Program`. Add a
`SelfType` marker type for `Self` (parser emits it; the checker/parser
substitute it with the impl's concrete type before the type ever reaches
later passes).

### 6.3 Parser (`internal/parser/parser.go`)
Two new top-level forms alongside `struct`/`enum`/`type`:
- `trait Name { <sig>; <sig>; }` — each `<sig>` is a function signature
  (no body). First param is required to be `self`; its type is recorded
  as `ast.SelfType{}`.
- `impl Trait for Type { <function>… }` — each function is parsed by the
  existing `parseFunction` machinery. The parser **synthesises a receiver
  clause**: the method's first param `self: Self` becomes the FuncDecl
  `Receiver`, with `Self` replaced by the impl's `for` type. The methods
  are appended to `prog.Funcs` (so modload + checker treat them as
  ordinary receiver methods), and the `ImplDecl` records `(Trait, Type,
  method-names, positions)` for the conformance + coherence pass.

This desugaring is why no later stage needs to know traits exist at
runtime.

### 6.4 Checker (`internal/checker/checker.go`)
- **Trait registry**: new `Info.Traits map[string]*ast.TraitDecl`.
  Register each `TraitDecl`; reject duplicate trait names.
- **Conformance** (after the receiver-hoist loop at `:1651`): for each
  `ImplDecl`, resolve the `for` type's canonical name (the same
  struct/enum/numeric-width naming the hoist uses), then check every
  trait method is registered in `Info.Methods` for that type with a
  signature matching the trait's (after `Self → Type`). Missing method →
  `E0xx "type T does not implement trait Tr: missing method m"`.
  Signature mismatch → `E0xx`. Extra method in impl → `E0xx`.
- **Coherence**: maintain a `map[traitName]map[typeName]Position` of seen
  impls. Duplicate `(Trait, Type)` → error citing both positions. Orphan
  check: `Trait` or `Type` must have `SourceModule == impl.SourceModule`
  (empty SourceModule = single-file/local = always OK).
- **Conformance record**: `Info.Impls map[string]map[string]bool`
  (`trait → set of type-names`) so Phase 2's bound-checking can ask "does
  `i32` implement `Display`?".

### 6.5 Bounded generics (Phase 2)
- Parser: extend the type-param list grammar `[T: Bound + Bound]` to
  carry per-param bounds. Add `Bounds map[string][]string` to `FuncDecl`
  (and later `StructDecl`).
- Checker first pass: when a method call's receiver is `ParamType{T}` and
  `T` has bound `Tr` declaring method `m`, type-check against the trait
  signature and **do not rewrite** the callee.
- Checker: at each generic call site, verify each concrete `TypeArg`
  satisfies the bound (`Info.Impls[Tr][typeName]`); else
  `E0xx "T = i32 does not implement Display"`.
- The monomorph recheck (already present) finishes dispatch. No new
  runtime lowering.

### 6.6 modload (`internal/modload/modload.go`)
- Aggregate `Traits` / `Impls` in `combine`; stamp `SourceModule`.
- Mangle `TraitDecl.Name` for non-entry modules exactly like struct
  names, and rewrite `ImplDecl.Trait` / the `for` type name through the
  same `rewriteStructName` path so cross-module references line up. (The
  impl *methods* are already ordinary receiver methods in `prog.Funcs`,
  so their mangling is handled.)
- Also rewrite each `FuncDecl.Bounds` trait name and the `ImplDecl.Trait`
  via `rewriteTraitNameAt` (own-module → `selfPrefix`, qualified
  `mod.Trait` → the imported module's prefix). Done in Phase 3; proven by
  multi-file e2e tests (`internal/e2e/traits_test.go`).

### 6.7 `derive` (Phase 4)
`@derive(Display, Eq)` on a struct/enum synthesises field-wise impls. The
checker already walks field layouts; generation is mechanical. This is
what makes traits *ergonomic* and is the lever that finally collapses the
`assert_eq_*` family.

## 7. Phasing

1. **Phase 1 (shipped):** lexer + AST + parser + checker registration,
   conformance, coherence. Direct method calls via impls work end-to-end
   on every backend. Tests at every layer.
2. **Phase 2 (shipped):** bounded generics `[T: Trait]` + deferred
   dispatch + bound-satisfaction checking. `v.to_string()` inside
   `show[T: Display]` resolves per-instantiation via the
   monomorphise-then-recheck loop; verified through the interpreter *and*
   the wasm backend. (Implementation detail: the checker's method
   registration was made idempotent so the monomorph re-check — which
   rebuilds `Info` from scratch — re-resolves already-hoisted methods.)
   The full `assert_eq_*` collapse waits on Phase 3, because the helpers
   live in `std/test` and need cross-module trait references.
3. **Phase 3 (shipped):** cross-module trait coherence. Trait names are
   mangled like struct names; `impl mod.Trait for …`, `[T: mod.Trait]`
   bounds, and `TraitDecl.Name` are rewritten through the same
   qualified-or-own logic so they line up in the combined program. The
   orphan rule + bound checks work across modules, and diagnostics
   demangle `mod__Name` back to `mod.Name`. (The checker's method
   registration carries a stamped receiver identity — `MethodRecv` /
   `MethodSimpleName` — so the monomorph re-check re-resolves
   cross-module mangled methods whose names the string can't be split
   on.) This unblocks collapsing the `std/test` `assert_eq_*` /
   `assert_neq_*` families onto one generic helper per family — done as
   its own focused PR since it changes `std/test`'s public surface.
4. **Phase 4 (shipped):** `@derive(Eq, Display, Ord)` on structs.
   `@derive(Trait, …)` synthesises a field-wise `impl` per trait — the
   generated method calls the trait method on each field
   (`self.f.eq(other.f)`, `self.f.to_string()`, `self.f.cmp(other.f)`),
   so derivation composes (a field type only needs to itself implement
   the trait). Lives in the Go checker's `synthesizeDerives` (run before
   the receiver-hoist + conformance passes; idempotent across the
   monomorph re-check). Verified through the interpreter and the wasm
   backend; nested-struct composition tested. Enums are supported too
   (variant-wise `match` synthesis for Eq / Display). The core `std/test`
   **array** families (`assert_eq_<T>_array`, `assert_at_<T>`,
   `assert_array_contains/not_contains_<T>`) have been collapsed onto
   generics over `[T: Eq + Display]` — the self-host monomorphiser
   learned `T[]` parameters + array-literal element inference. The
   `@derive(Ord)` for enums (variant-tag order, then lexicographic
   payloads) and `impl Ord for string` shipped, and the `sorted_*` /
   `set_eq` / `subset` / `unique` array families are now collapsed too
   (over `[T: Ord + Display]` / `[T: Eq + Display]`). The **map** families
   (`assert_map_len` / `assert_map_has` / `assert_map_lacks` /
   `assert_eq_map`) are now collapsed as well, over
   `[K: Eq + Display, V: Eq + Display]` — this needed the self-host
   monomorphiser to grow MULTI-parameter support (infer K and V from a
   single `Map[K, V]` argument, mangle the clone with both concrete types
   joined `f__i32__string`); see §7a.
5. **Phase 5:** **opaque types — shipped.** `pub opaque struct Name { … }`
   exports the type name + its methods but keeps fields private outside
   the declaring module: cross-module field reads and struct-literal
   construction are rejected (the checker compares the struct's
   SourceModule against the access site's). The module provides
   constructors/accessors; this is the ADT discipline that pairs with
   trait impls. `dyn Trait` objects remain a follow-up if a
   heterogeneous-collection use case appears.
6. **Phase 6 (shipped): parametric impls + generic `@derive`.**
   `impl[T: Bound] Trait for Box[T]` — one blanket impl that covers every
   instantiation of a generic type. The parser parses a leading
   `[T: Bound]` after `impl` (shared `parseTypeParamList` helper, reused
   by generic functions / methods); each desugared method inherits the
   impl's type params + bounds, so the receiver-hoist registers it as a
   generic method that monomorphises per instantiation. The checker
   resolves the `for` type's own `T` references to `ParamType`
   (`resolveTypeNames` now walks `prog.Impls`) so the conformance
   signature comparison lines up with the (generic) hoisted receiver.
   `monomorph.Run` drops parametric impls before the re-check — their
   generic `for` type has been monomorphised away, so a re-check would
   raise a spurious orphan/missing-type error against a type that no
   longer exists; the plain (concrete) impls survive and re-validate. On
   top of this, `synthesizeDerives` emits a *parametric* impl for a
   generic struct/enum (`@derive(Display) struct Box[T]` →
   `impl[T: Display] Display for Box[T]`), binding every type parameter
   by the derived trait so the field/variant-wise body dispatches through
   the bound. Generic-enum derive (previously rejected outright) and
   multi-parameter generic structs both work. Verified on all four
   backends (interp / arm64 / x86-64 / wasm); a payload-less variant of a
   generic enum (`Nil`) still needs a type annotation at a bare
   `.method()` call site since `T` is otherwise unconstrained.
   Self-host parity (the self-host parser/monomorphiser learning
   `impl[T]`) is a follow-up — the Go compiler is the reference.

## 7a. Self-hosting the trait feature

The self-hosted compiler (`examples/self_host/*.fern`) must compile a
trait-using `std/test` for the `assert_eq_*` collapse to land without
regressing the self-host gates. It needs traits in two slices:

- **Self-host slice 1 (shipped):** lexer + parser. Recognise
  `trait`/`impl`, desugar impl methods to receiver-methods, swallow
  `[T: Bound]`. The self-host dispatches `obj.method()` dynamically by
  the receiver's runtime *shape pointer* (a heap header), so concrete
  impls and struct-receiver bounded generics work for free.

- **Self-host slice 2 (shipped): monomorphise bounded generics.**
  The dynamic dispatch reads a shape pointer from the receiver value —
  which heap values (struct/string) carry but **unboxed primitives
  (i32/i64/u32/u64/bool) do not**. So `assert_eq(1, 2)` — a bounded
  generic whose `T` is i32 — fell into the struct dispatch path,
  dereferenced the integer as a pointer, and crashed. The fix
  monomorphises: `parser.monomorphize_module` (run inside
  `module_with_builtins`, so every asm driver gets it with no checker)
  walks call sites, infers each instantiation's concrete type from the
  argument bound to the type variable, clones `f` → `f__<type>` with the
  type variable substituted in params / return / `var` annotations, and
  rewrites call sites; a worklist covers clones that call other bounded
  generics. The clone's concrete receiver then routes through the
  emitter's static-primitive dispatch — no emitter change. Unbounded
  generics keep their erasure. Tested on x86-64 + arm64
  (`internal/e2e/self_host_traits_test.go`): primitive, struct,
  multi-type, and mixed-primitive-and-struct instantiations.

- **Self-host slice 3 (shipped): MULTI-parameter monomorphisation.**
  The `Map[K, V]` assertion collapse needs a bounded generic over *two*
  type parameters, with both inferred from a single argument. The
  monomorphiser was generalised from one type variable to N: `infer_inst`
  builds the per-parameter bindings via `bind_unify` (which matches a
  parameter-type pattern against the argument's inferred type — a bare
  variable `T ⊢ i32`, an array element `T[]` vs `i32[] ⊢ T=i32`, and a
  same-base composite `Map[K, V]` vs `Map[i32, string] ⊢ K=i32,V=string`)
  and returns the concrete types joined `__` in declaration order;
  `subst_ty` substitutes every variable (including inside composites like
  `Map[K, V]` → `Map[i32, string]`, so the emitter's static map-tag
  dispatch sees concrete key/value types); `clone_bg` splits the joined
  key back into per-parameter pieces and mangles `f__i32__string`. The
  single-parameter path is the N=1 special case, unchanged in behaviour.
  Tested via the new `trait-bounded-generic-two-params` case plus the
  full `map_eq_and_predicates` / `batch8` suites through the self-host
  gate.

- **Self-host slice 4 (shipped): parametric impls over struct-typed
  type parameters.** `impl[T: Bound] Trait for Box[T]` now parses in the
  self-host: `parse_impl_decl` consumes the leading `[T: Bound]` list
  and stamps the impl's type params onto each desugared receiver method;
  the `for` type keeps its `Box[T]` spelling. The x86-64 + arm64
  emitters gained `base_type_name` (mirroring the wasm backend's
  helper), applied at the method-symbol-formation and
  dispatch-shape-compare sites so a `Box[T]` receiver strips to the base
  struct name `Box` — which is what a `Box{…}` value's runtime shape
  string carries. This makes a parametric impl dispatch end-to-end when
  the type parameter is **struct-typed**: a `Box[Inner]` value's
  `box.m()` finds the impl method, whose body calls `self.v.m()` on the
  struct-typed field, which carries its own shape pointer and dispatches
  dynamically. **Primitive / string `T`** (e.g. `Box[i32]`,
  `Box[string]`) still needs monomorphisation — the self-host's dynamic
  dispatch can't read a shape off an unboxed primitive or a
  length-prefixed string, exactly the boundary the bounded-generic
  monomorphiser (slice 2) handles for plain generic functions. Tested
  via `trait-parametric-impl-struct-elem` on both backends; the
  self-host fixpoint + stdtest gates confirm the compiler still builds
  itself.

  **Boundary for primitive/string `T` (investigated, deferred):**
  completing this is *not* just "clone the generic receiver method." The
  method body does `self.v.m()` where the field `v` is declared `T`; to
  dispatch that statically for `Box[i32]` the compiler must know
  `self.v` is `i32` — i.e. it must monomorphise the generic STRUCT so the
  instantiated field types are concrete (`Box[i32]` becomes a `Box__i32`
  whose `v: i32`). The self-host deliberately ERASES generic structs
  (every `Box[…]` shares one shape; fields are uniform 8-byte slots), so
  neither the monomorphiser nor the emitter tracks a generic struct's
  field types under instantiation. Generic-struct monomorphisation is a
  large departure from that erasure model; it is the real prerequisite
  for primitive/string-`T` parametric impls (and would also be the
  vehicle for generic `@derive`). Deferred until a use case justifies
  the architectural cost — the struct-typed-`T` slice above is the
  natural milestone within the erasure model.

- **Self-host slice 5 (shipped): `dyn Trait` + `@derive` on structs.**
  Two more of the Go-side trait features ported toward retiring the Go
  compiler. **`dyn Trait`**: a `dyn` lexer keyword + a `parse_type_name`
  case (coarse `"dyn <trait>"` spelling); dispatch is free — the
  self-host already shape-dispatches `d.m()` on any heap value. The one
  real fix was a pre-existing bug: the generic `"array"` tag lost its
  element type, so a `for x in xs { x.m() }` loop var defaulted to
  `i32` and mis-dispatched to the primitive path; it now binds
  `"unknown"` → runtime-shape dispatch (fixes struct arrays too).
  **`@derive`**: an `@` punctuator in the lexer + a `@derive(Trait, …)`
  attribute parsed in `parse_module`; `synth_struct_{display,eq,ord}`
  build the same field-wise receiver methods the Go `synth*` functions
  emit (byte-identical Display, so the differential oracle agrees).
  Structs only — enums desugar to struct-unions here, so enum `@derive`
  needs variant-wise synthesis over the variant structs (follow-up), and
  generic-struct `@derive` waits on generic-struct monomorphisation
  (slice-4 boundary). Both work for the cases the erasure model supports
  (struct/enum concrete types; primitive/string fields use their
  intrinsic / explicitly-impl'd methods). Tested via
  `trait-dyn-object-heterogeneous`, `trait-struct-array-loop-method`,
  `trait-derive-struct-{eq,ord,display-nested}` on x86-64 + arm64, plus
  `examples/tests/derive_test.fern` through the import-resolving stdtest
  gate (real `core/cmp`).

- **Self-host slice 6 (shipped): enum methods + enum `@derive`.** Enum
  receiver methods (`function (s: Shape) area()`) didn't work in the
  self-host: a value's runtime shape is the VARIANT (`Circle`), but the
  method is registered on the enum type (`__method_Shape_area`), so the
  shape-compare never matched and the call trapped. Fixed with an
  **enum-method fallback** in both native emitters: shape-compare arms
  are emitted only for struct/variant receiver types (`is_struct_decl_name`);
  a method whose receiver type is an enum (`is_enum_recv` — not a struct
  decl and not a primitive, the latter exclusion essential since
  `impl Eq for i32` carries receiver_type `"i32"`) is the fallback call,
  taken when no variant shape matched — its internal `match (self)` then
  dispatches on the variant. On top of that, `@derive(Eq, Display)` on
  enums synthesises variant-wise methods (`synth_enum_{display,eq}`,
  matching the Go `synthEnum*`): Display renders `Variant(payload)` /
  `Variant`, Eq matches both values and compares the payload. The parser
  now threads the enum NAME through `EnumResult` (previously discarded)
  so the synthesised methods name their receiver type. Self-host enum
  variants carry a single payload (`__ev`), so multi-payload variants
  render/compare their first field. `@derive(Ord)` for enums followed
  (`synth_enum_ord`: variant-declaration order decides cross-variant, the
  payload decides within). Tested via `trait-enum-method`,
  `trait-derive-enum-{display,eq,ord}` on x86-64 + arm64, plus the enum
  section of `examples/tests/derive_test.fern` through the stdtest gate —
  so `@derive(Eq, Display, Ord)` reaches full parity for structs AND
  non-generic enums on the native (x86-64 + arm64) self-host backends.

- **Self-host slice 7 (in progress): the wasm backend.** The wasm
  self-host backend (`examples/self_host/wasm.fern`) dispatches methods
  STATICALLY by the receiver's known type (`struct_type_of` →
  `$Type__method`), unlike the native backends' runtime shape-compare,
  so the trait fixes there don't port directly. First fix landed: the
  `to_string` intrinsic deferred to a user method only when the receiver
  is int/i64/string — a struct/enum receiver with its own `to_string`
  (hand-written or `@derive(Display)`) was wrongly formatted as an
  integer. Gated on `struct_type_of(recv)` having a `to_string` method,
  so **struct `to_string` + `@derive(Display)`** (incl. nested structs)
  dispatch correctly on wasm. Then **user-defined enums** landed: the
  variant-tag scheme is `struct_id` (a struct's index in the table,
  stored at offset 0, read by `match`), so a positional variant call
  `Circle(3)` now builds `[struct_id@0, payload@4]` like a struct literal
  (only `Option`/`Result` were special-cased before, leaving an undefined
  `$Circle` call); a bare unit variant (`Nil`) builds its `[struct_id@0]`
  tag box; `match` binds an enum variant's payload at offset 4 (a
  `__ev`-bearing struct) vs the whole value for a `type E = A | B`
  struct-union; and an enum-typed var records the ENUM name (not the
  variant from its initializer) so enum methods dispatch. With those,
  **user enums + enum methods + enum `@derive(Display)`** work on wasm.
  Then **primitive-receiver user methods** landed: when the receiver
  isn't a struct/enum (`struct_type_of` empty) the dispatch picks the
  primitive type (i32 default, or string/i64) and calls `$<prim>__method`
  if a user impl exists — so `self.x.eq(other.x)` on an i32 field reaches
  `impl Eq for i32`. A match arm `Has(b)` over an enum variant now types
  `b` as the payload (the `__ev` field type) rather than the variant, so
  a primitive payload routes through that dispatch. With these,
  **`@derive(Eq/Ord)` on structs and on var-typed enums** work on wasm.
  The one remaining wasm hole: an INLINE variant-call receiver
  (`Has(5).eq(…)`) — `struct_type_of` can't recover the enum type from a
  bare variant constructor (the variant→enum map was dropped at parse),
  so it falls to the i32 path and compares pointers; binding to a typed
  var first (`var h: Opt = Has(5); h.eq(…)`) works. And `dyn Trait` on
  wasm still needs genuine runtime dispatch (the backend is
  static-dispatch).

## 7b. The `std/test` collapse (landed)

The scalar `assert_eq_<T>` / `assert_neq_<T>` / `assert_{lt,le,gt,ge}_<T>`
families collapsed onto generic `assert_eq` / `assert_neq` / `assert_lt`
… over a new `core/cmp` module (`Display` / `Eq` / `Ord` traits + their
primitive impls). Both compilers green. Beyond the trait engine + the
self-host monomorphiser, landing it took:

- **Go checker:** `boolean` as a method-receiver type (so `bool`
  implements traits); and settling a polymorphic numeric literal against
  a type variable already bound by an earlier argument
  (`assert_eq(a + b /* i64 */, 8000000000)`).
- **Self-host monomorphiser:** module-qualified call sites
  (`test.assert_eq(…)` is an `ExprFieldAccess` callee, not an
  `ExprIdent`); and multi-argument instantiation inference (infer the
  type from *any* `T`-typed argument, so
  `assert_eq("x".to_upper(), "X")` resolves via the literal).
- **Self-host emitters (asm + asm_arm64):** the primitive
  static-method-dispatch gate listed only `i32/string/bool/f64`; widened
  to all integer tags (`is_int_tag`) + `f32`, so `i64`/`u32`/`u64`
  receivers dispatch statically instead of crashing on the struct
  shape-pointer path.

The **array** families (`assert_eq_*_array`, `assert_at_*`,
`assert_array_contains/not_contains_*`, `assert_sorted_*`, `set_eq` /
`subset` / `unique`) then collapsed over `[T: Eq + Display]` /
`[T: Ord + Display]`, and the **map** families (`assert_map_len`,
`assert_map_has`, `assert_map_lacks`, `assert_eq_map`) over
`[K: Eq + Display, V: Eq + Display]` once the self-host monomorphiser
grew multi-parameter support (slice 3 above). The map helpers render
values via `Display`, so a string-valued failure now prints unquoted —
the `batch8` wrong-value check was updated to match.

## 8. Testing

Per the engineering bar (every feature ships with tests at the layer it
touches):
- **Parser** (`parser_test.go`): trait decl, impl decl, the receiver
  synthesis, error cases (missing `self`, `impl` without `for`).
- **Checker** (`checker_test.go`): conformance pass/fail (missing
  method, signature mismatch, extra method), duplicate-impl, orphan-rule
  rejection.
- **e2e** (`internal/e2e`): a program that declares `trait Display` +
  `impl Display for Point`, calls `p.to_string()`, runs on the
  interpreter and prints the expected string.
- Full suite (incl. WASM e2e) must stay green; never regress.
