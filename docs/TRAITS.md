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
  helper per family."* `time.fern:149` echoed the same wish, as did
  `std/format` until its `Display`-accepting entry points landed (#2684).
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

- A **trait** is a named set of *signatures*. A **method**'s first
  parameter is `self: Self`; `Self` is a contextual type that stands for
  the implementing type. A signature with no leading `self` is an
  **associated function** (`ast.TraitMethod.Assoc`) — `std/num`'s
  `Num { function zero(): Self; }` and `std/convert`'s
  `From { function from(v: T): Self; }` are both this form. It is called
  as `T.zero()`, never on a value: `x.zero()` is E021.
- An **impl** provides bodies for exactly the trait's methods, with `Self`
  bound to the `for` type. Every *abstract* method must be present;
  signatures must match (modulo `Self` → `Type`); no extra methods are
  allowed.
- `trait` may be `pub`. Impls inherit visibility from the type/trait;
  there is no `pub impl`.

### Inherent impls (`impl Type { … }`)

An impl block with **no `for Trait`** is an *inherent* impl — methods and
associated functions attached directly to a type, with no trait to conform
to. This is the home for constructors and static helpers that don't belong
to any trait (issue #2700):

```fern
struct Point { x: i32, y: i32 }

impl Point {
    function origin(): Self { return Point { x: 0, y: 0 }; }   // associated fn
    function make(x: i32, y: i32): Point { return Point { x: x, y: y }; }
    function sum(self: Self): i32 { return self.x + self.y; }   // method
}

var p: Point = Point.make(3, 4);   // Type.f(args) — associated function
var n: i32 = p.sum();              // p.method()    — ordinary method
```

The desugaring is identical to a trait impl: a receiver-less function
becomes an **associated function** called as `Type.f(args)`, a `self`-taking
function becomes an ordinary **method**, and `Self` resolves to the impl
type. Inherent impls may be generic (`impl[T] Box[T] { … }`). The only
difference from a trait impl is that there is no conformance check — any set
of functions is allowed.

### Default methods

A trait method may carry a `{ … }` body instead of ending at `;`. That
body is a **default**: an impl that omits the method inherits a copy
(with `Self` substituted to the impl type); an impl may still provide its
own to override it.

```fern
trait Greet {
    function name(self: Self): string;                 // abstract — every impl must provide it
    function greeting(self: Self): string {            // default, expressed via the abstract method
        return "hello, " + self.name();
    }
}

struct Dog { age: i32 }
impl Greet for Dog { function name(self: Self): string { return "rex"; } }   // greeting inherited

struct Cat { age: i32 }
impl Greet for Cat {
    function name(self: Self): string { return "felix"; }
    function greeting(self: Self): string { return "meow from " + self.name(); }   // override
}
```

This is the single-required-method-plus-derived-helpers shape that makes
Rust's `Iterator` usable. It is a pure front-end feature: the checker
materialises each inherited default as an ordinary receiver method on the
impl type (`synthesizeTraitDefaults`, run right after `@derive`
synthesis), so the hoist, conformance, dispatch, monomorphisation and
codegen paths are unchanged — a default reached through a `T: Greet`
bound monomorphises exactly like a written method. Defaults work on the
interpreter and every native/wasm backend; `Self` *as a value type
inside* a default body is treated the same as inside a hand-written impl
method. The self-hosted compiler has matching support (same-module) —
see §7a, self-host slice 10.

Once `impl Display for Point` exists, **`p.to_string()` works for a
`Point` value through the ordinary method-dispatch path** — an impl
method *is* a method. That is the whole of Phase 1's runtime story, and
it needs no codegen/IR/interp changes.

Bounded generics (Phase 2) let a function abstract over any `T` that
implements a trait:

```fern
pub function assert_eq[T: Display + Eq](actual: T, expected: T): TestOutcome {
    if actual.eq(expected) { return Pass; }
    return Fail("expected " + expected.to_string()
                + " but got " + actual.to_string());
}
```

### Generic traits

A trait may take type parameters, written `trait Name[T, …]`, referenced in
its method signatures. Each impl binds them positionally with
`impl Name[Arg, …] for Type`:

```fern
trait From[T] { function from(v: T): Self; }
struct Celsius { deg: i32 }
impl From[i32] for Celsius { function from(v: i32): Self { return Celsius { deg: v }; } }

var c: Celsius = Celsius.from(20);   // associated function, T=i32

trait Container[T] { function get(self: Self): T; }
impl Container[i32] for IntBox { function get(self: Self): i32 { return self.v; } }
```

The conformance check binds the trait's `TypeParams` to the impl's
`TraitArgs` (`From[i32]` → `T=i32`) and substitutes them into the trait's
method signatures before comparing against the impl's concrete methods
(`substByName` + the usual `Self`→impl-type substitution). A wrong arity
(`impl From for …`) or a mismatched method binding is rejected.

Implementation: `TraitDecl.TypeParams` (parser: `[T,…]` after the trait
name) and `ImplDecl.TraitArgs` (parser: `[Arg,…]` after the trait name in
the `impl`); modload mangles struct/enum names in `TraitArgs`; the checker
substitutes them in the conformance pass. Dispatch is unchanged — calls
resolve to the impl's concrete monomorphic method.

**Bounded generics over a generic trait** are supported too — a bound may
carry the trait's type arguments:

```fern
function describe[T: From[i32]](proto: T, v: i32): T {
    return T.from(v);   // resolves against `from(v: i32): T`
}
```

`FuncDecl.BoundArgs` carries the bound's args parallel to `Bounds`
(`Bounds["T"]=["From"]`, `BoundArgs["T"]=[[i32]]`); the parser reads
`[Arg,…]` after each bound trait name, `resolveTraitMethodForParam`
substitutes them into the bound trait's method signatures
(`substTraitMethodTypeParams`), and modload mangles struct/enum names in
the bound args. The bound's **arity** is validated against the trait's type
parameters. Trait-bound *satisfaction* at the call site is **precise**: the
type argument must have an impl of the trait whose `TraitArgs` match the
bound's — a `T: From[i32]` bound is satisfied by `impl From[i32] for T` but
not `impl From[f64] for T` (`Info.ImplTraitArgs` records each impl's args;
`typeArgsEqual` compares). A non-generic bound (no args) is unaffected.

**Remaining follow-up:** `dyn`-generic-traits (`dyn Container[i32]`).

### Supertraits

A trait may require other traits with a `: Trait + Trait` clause after its
name. `trait Ord: Eq` means **Eq is a supertrait of Ord**:

```fern
trait Eq  { function eq(self: Self, other: Self): boolean; }
trait Ord: Eq { function lt(self: Self, other: Self): boolean; }
```

Two consequences (Rust's semantics):

- **Conformance**: `impl Ord for P` is legal only if `impl Eq for P` also
  exists (checked transitively, and independent of impl order). The error
  reads `impl Ord for P also requires \`impl Eq for P\` (supertrait of Ord)`.
- **Bound expansion**: a `T: Ord` bound also exposes the supertraits'
  methods, so a generic over `Ord` can call `eq`:

  ```fern
  function rank[T: Ord](a: T, b: T): i32 {
      if a.eq(b) { return 0; }       // Eq method, reached via Ord's supertrait
      if a.lt(b) { return -1; }
      return 1;
  }
  ```

Supertrait names may be qualified (`mod.Trait`) and are mangled like any
other trait reference. The supertrait graph must be acyclic and each
supertrait must name a real trait (both are checked: `cyclic supertrait`
/ `unknown supertrait`). Implemented in the checker
(`expandTraits` / `collectTraitSupers` drive bound expansion;
`traitInItsOwnSupers` the cycle check) — `core/cmp`'s traits stay flat for
now, so no existing impl is forced to gain a supertrait. The self-host
parser skips the supertrait clause (it dispatches by receiver type and
carries no conformance), so supertrait programs still compile there.

## 3a. Display spine: `print` / `write` / `eprint` (#2696)

`Display` is the language's stringification spine: a type implements it by
providing `to_string(self: Self): string`. The output builtins route
through it, so any `Display` value can be printed directly — no
stringify-first dance:

```fern
import "std/i32";
import "core/cmp";

@derive(cmp.Display)
struct Point { x: i32, y: i32 }

function main(): i32 {
    var p: Point = Point { x: 1, y: 2 };
    print(42);   // was: print((42).to_string())
    print(p);    // was: print(p.to_string())  →  "Point { x: 1, y: 2 }"
    return 0;
}
```

`print(x)` / `write(x)` / `eprint(x)` accept any `T` whose type carries a
`to_string(): string` in scope — a `@derive(Display)` / `impl Display`
type, a scalar with its stdlib `to_string` imported, or a bounded generic
`T: Display`. A plain `string` argument is passed through unchanged; a
non-string argument is rewritten by the checker to `arg.to_string()` (the
same desugar f-strings use), so the value stringifies through the trait
before reaching the string-only runtime helper. A type with no `to_string`
in scope is rejected with a `Display`-specific diagnostic. This is a
checker-stage rewrite only — the formatter still renders the source
`print(x)` form, and every backend sees an ordinary `print(string)` call.

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

## 4a. Bound-driven inference (#2691)

A fully-generic iterator collector is generic over **both** the iterator
type and its element type:

```fern
function last[T, I: Iterator[T]](it: I, dflt: T): T { … }

last(RangeIter { cur: 0, end: 5 }, -1)   // T = i32, I = RangeIter
last(BoolSeq { n: 2 }, false)            // T = boolean, I = BoolSeq
```

Here `T` appears in a parameter (`dflt: T`) and the return type, but the
*defining* occurrence is inside another parameter's parametrised-trait
bound: `I: Iterator[T]`. Ordinary argument-driven inference pins `I` from
`it`'s type, but it cannot pin `T` from `dflt` alone in the general case
(an i32 literal could be many things), and a collector like
`count[T, I: Iterator[T]](it: I): i32` mentions `T` *only* in the bound.
Without help the checker reported `E040: could not infer type parameter T`.

**Bound-driven inference** recovers `T` from the impl the bound resolves
to. After the normal pass binds `I` to a concrete type, for each bound
type-parameter already pinned to a concrete type the checker unifies the
bound's trait arguments (which mention `T`) against that type's impl's
trait arguments. `I = RangeIter` and `impl Iterator[i32] for RangeIter`
give `Iterator[T]` vs `Iterator[i32]`, binding `T = i32`. A small fixpoint
loop lets one bound parameter feed another (`[T, U, I: Map[T, U]]`).

The lever is that bound type-arguments are normalised so a leaf whose name
is a type parameter is a `ParamType`, not a same-named nullary `StructType`
(the parser can't tell them apart) — see `normalizeParamRefs`,
`bindBoundParam`, and `substBoundArg` in `internal/checker/checker.go`.
Trait-bound satisfaction (E021) then resolves the bound's `T` through the
inferred substitution before comparing against the impl's concrete args.

On the **self-host IR path** no inference is needed: an *unbounded* type
parameter like `T` is erased (the ABI is a uniform 8-byte slot, so one
monomorphic body fits every element type) and the function monomorphises
on the bounded `I`. So once the parametrised-trait bound itself parses
(#3558) the collector lowers through the IR path unchanged. The native
backend monomorphises fully (no erasure), so it needs the inferred `T`.

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

### 5.1 Which impl, when two traits share a method name

Coherence pins `(Trait, Type)`. It does not pin `(Type, method name)`:
two different traits may each declare `scale`, and one type may
implement both. That is legal, and the second impl is not a
redeclaration — the checker keys trait methods by
`<Trait>.<Type>.<name>`, so both stay individually addressable.

The *call* is what must be unambiguous. `v.m()` with `v: T` ranks the
traits in `MethodOwners["T.m"]`:

- **rank 0** — the trait is declared in the calling module, or that
  module's own `import` list names the trait's module;
- **rank 1** — everything else: a trait reachable only because some
  module you imported imported it.

Exactly one candidate at the best occupied rank resolves the call; two
or more at that rank is `E074`. The ranking reads the *direct* import
map, not the transitive closure — the closure would count `std/num` as
in scope for a program that only ever wrote `import "std/i32"`, tying a
user's own trait with one they never named. A lower-ranked trait is
still a live candidate, never filtered out: a bounded generic is
re-checked by the monomorphiser from the module that *defined* it,
where the receiver's module is not imported, and a hard visibility
filter there would drop the only candidate.

Inside a generic body the *bound* answers first and the ranking never
runs: `v.m()` with `v: T` and `[T: SomeTrait]` dispatches through
`SomeTrait`, and that answer is carried on the call site so the
monomorphiser's re-check of the substituted clone lands on the same
impl. Without it the re-check happens with a concrete receiver in the
module that defined the generic, where two traits it declares itself
both rank 0 — a call the first check resolved would become `E074`
inside generated code the source cannot point at (§4).

An inherent method colliding with a trait-provided one of the same name
is `E074` too, reported at the declaration rather than at a call. It is
not resolved by rank and the inherent one does not shadow: silent
shadowing would make `p.m()` mean the trait method inside a generic body
(dispatch through the bound) and the inherent method at a concrete call
site — one spelling, two meanings.

So the invariant is narrower than "dispatch is independent of who
imports whom", which is what coherence alone would give: **among the
candidates a module can see, dispatch is unambiguous or it is an
error.** Never a silent pick between two answers.

An import only ever *adds* candidates, and a call whose answer already
ranks 0 cannot be displaced by one: a new rank-1 candidate loses, and a
new rank-0 one ties, which is `E074`. The one way an import changes
which method runs rather than rejecting the call is when the previous
answer ranked 1 — reachable only through somebody else's imports — and
the new import names a trait that provides the same method for the same
type. That is the motivating case read forwards: naming a trait yourself
is how you say which one you meant, so it wins over one you never
mentioned. An import that introduces no second provider of the same
`(Type, method name)` never changes dispatch at all.

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
`@derive(Trait, …)` on a struct/enum synthesises field-/variant-wise
impls. The checker already walks field layouts; generation is mechanical.
This is what makes traits *ergonomic* and is the lever that finally
collapses the `assert_eq_*` family.

Seven traits are derivable today (`deriveKind`, `synthesizeDerives`):

| Trait     | Synthesised method      | Shape |
|-----------|-------------------------|-------|
| `Eq`      | `eq(self, other)`       | field-wise `&&` / variant match |
| `Ord`     | `cmp(self, other): i32` | lexicographic fields; variant-declaration order |
| `Display` | `to_string(self)`       | `Name { f: …, … }` / `Variant(p)` |
| `Debug`   | `to_debug(self): string`| structural like `Display`, but strings render QUOTED (`label: "hi"`, `Tag("ab")`) — the `{:?}` half of the Display/Debug split |
| `Hash`    | `hash(self): i32`       | `h = h*31 + f.hash()`; enum seeds with the variant tag |
| `Json`    | `to_json(self): string` | JSON object; enums externally tagged |
| `Default` | `default(): Self`       | zero value — scalars' zero literal, nominal fields delegate to *their* `default()`; an enum defaults to its first variant (payloads defaulted) |

Each composes through the same trait on every field/payload, so a type is
`@derive`-able as soon as its fields are — and a generic type derives a
*parametric* impl (`@derive(Hash) struct Box[T]` → `impl[T: Hash] Hash for
Box[T]`). For every kind whose synthesised body calls the trait method on
each field — all but `Default` — that requirement is enforced up front
(#5392): a field (or variant payload)
type with no impl, no derive of its own, and no covering method set draws
one positioned E021 at the deriving decl (`cannot @derive(Ord) for Foo:
field x of type i32 does not implement Ord — add \`impl Ord for i32\``)
and the broken method is not synthesised. `Default` is excluded because it
composes through zero literals and `Type.default()` rather than a call on
the field value, and reports its own per-field gap from `synthDefault`.
Without the gate the non-conforming field escapes into the synthesised
body and surfaces as a position-less E043 blaming a missing *field* named
after the trait's method (`struct Bare has no field "to_debug"`, at 0:0
with no file) — which is what `Display`/`Debug`/`Json` did until the gate
grew past its original `Eq`/`Ord`/`Hash` list. Both checkers enforce it — the
native pre-check is `preCheckDeriveFields` (checker.go), the self-host's
is `e021_derive_field_diags` (checker.fern, reading the `derives` list
the parser stamps on each StructDecl).

The two agree code-for-code on the *broken* path as well. They get there
differently: native skips synthesis once the pre-check fires, so the
ill-typed body never exists, while the self-host synthesises derived bodies
at **parse** time — before any `impl` is known — and so cannot decide at
synthesis time. Instead `e021_derive_field_diags` returns the
`Type.method` key of every derive it condemned, and `check_module`'s body
loop declines to check exactly those synthesised functions
(`derive_body_suppressed`), which lands on the same end state: one
positioned E021, no trailing E043. Only synthesised methods sit at 0:0, so
a user-written method of the same name is still checked, and a genuine bad
field access elsewhere is untouched.

This spans all six field-wise kinds. `Eq`/`Ord`/`Hash` were long thought
unaffected because their bodies render inline (`==` / `<`) — but that holds
only for a *scalar* field; over a nominal one `Ord`'s body calls `.cmp()`
and diverged identically. The differential suite pins both paths
(`derive-display-impl-ok`, `derive-ord-field-broken` and siblings in
`self_host_checker_codes_test.go`).

One divergence remains, and it is a synthesis-*shape* difference rather
than a gate one: the self-host renders `Debug` type-directed, sending
scalar fields through `to_string` rather than `to_debug`, so a scalar
carrying only a `Debug` impl still disagrees. The differential cases use a
nominal field to stay clear of it.

`Eq`/`Ord`/`Display`/`Debug`/`Hash`/`Default` live in `core/cmp`;
`Json` in `std/json` (it returns canonical JSON text and reuses the
`JsonValue` encoder's string escaper). `Eq`/`Ord`/`Display`/`Debug`/`Hash`/
`Json` are mirrored in the self-hosted compiler's `synth_*`; `Default` is
native-only so far (its trait method is an *associated function* — see §6.8
— which the self-host frontend doesn't parse yet).

`Debug`'s self-host `synth_*` renders **type-directed** (numeric/boolean
scalars via `to_string`, strings quoted inline, nominal fields via their
own `to_debug`) rather than routing primitives through a `Debug` trait
method — the self-host discards `impl` bodies, so `(i32).to_debug()` has no
target there. The native checker instead resolves the primitive
`impl Debug for {i32,…,string,boolean}` impls in `core/cmp` (and quotes /
escapes strings); both produce identical output.

### 6.8 Associated functions
A trait method declared with **no `self` receiver** is an *associated
function*, called on the type rather than a value — the constructor /
static-method shape. It can be called either dot-qualified (`Type.f(args)`,
like `Color.Red`) or with the path separator `Type::f(args)` (#2700); both
parse to the same `FieldAccess` (`PathSep` records which separator was
written so the printer round-trips it) and resolve identically:

```fern
trait Default { function default(): Self; }
impl Default for Point { function default(): Self { return Point { x: 0, y: 0 }; } }
var p: Point = Point::default();         // `Self` resolves to Point (`::` or `.`)
function mk[T: Default](): T { return T::default(); }  // generic constructor
```

The `::` separator works for any namespaced reference, not just associated
functions: a module-qualified call (`helpers::add5(10)`) or const
(`helpers::BONUS`) is the path-style spelling of `helpers.add5(10)` /
`helpers.BONUS`. It's pure surface syntax — the checker / modload are
separator-agnostic. (The library-wide `Type::ctor` *convention* + the
`json.json_encode` stutter cleanup from #2700 are follow-ups; this lands
the mechanism.)

The parser marks a receiver-less trait/impl method (`TraitMethod.Assoc` /
`FuncDecl.AssocType`); the checker hoists it to `__assoc_<Type>_<name>`
(registered under the `Type.name` key), resolves a `Type.f()` call (a
`FieldAccess` on a type name, not a value) to the flat name with no
receiver prepended, and — for a `T.f()` on a bounded type parameter —
defers like a bounded method, with monomorph rewriting the target to the
concrete type. `default` is usable as a member name even though it's a
switch keyword (`expectMemberName`). This is what `@derive(Default)` builds
on. Native-only so far; self-host parity is a follow-up.

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

7. **Phase 7 (shipped): parametric impls of a GENERIC trait.**
   `impl[T] Iterator[T] for ArrayIter[T]` — a parametric impl whose own
   type parameter is passed as the *generic trait's* type argument. This
   combination (distinct from Phase 6's parametric impl of a *non-generic*
   trait, and from a *concrete* `impl Iterator[i32] for Range`) needed two
   fixes. **Checker:** the conformance compare built `want` from the trait
   method with the trait's type params bound to the impl's raw `TraitArgs`,
   where the param still reads as an unresolved `StructType("T")`, while the
   hoisted method's `got` side carries the resolved `ParamType("T")`; both
   print as `T` but `ast.Equal` separates the two node kinds, so the compare
   spuriously failed. Both sides are now canonicalised through the impl's
   type-param set before comparison, and the generic-trait *bound* check
   recovers the binding (`T=i32`) by unifying the impl's `for` pattern
   against the concrete argument (`ArrayIter[i32]`) before matching trait
   args. **Monomorphiser:** the call-driven worklist clones a parametric
   method only at a direct concrete call site; a method reached ONLY through
   a trait bound inside a generic combinator (`it.next()` in
   `sum[I: Iterator[i32]]`) had none, so the post-monomorph re-check could
   not resolve it. `monomorph.Run` now, for every concrete instantiation of
   a parametric impl's `for` type, clones the impl's methods under the
   receiver-dispatch name (`__method_ArrayIter__i32_next`, with `MethodRecv`
   re-pointed at the concrete type) and synthesises a concrete `ImplDecl`
   per instantiation so the re-check records the conformance for trait
   dispatch. Verified on interp / x86-64 / wasm; this is what makes
   `core/iter`'s `ArrayIter` (and thus every combinator over arrays / map
   keys / map values) lower. Self-host parity is a follow-up — the Go
   compiler is the reference.

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
  and stamps the impl's type params — with their bounds, which run
  parallel — onto each desugared receiver method;
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

  **Boundary for primitive/string `T` (CLOSED by slice 8 below):**
  completing this is *not* just "clone the generic receiver method." The
  method body does `self.v.m()` where the field `v` is declared `T`; to
  dispatch that statically for `Box[i32]` the compiler must know
  `self.v` is `i32` — i.e. it must monomorphise the generic STRUCT so the
  instantiated field types are concrete (`Box[i32]` becomes a `Box__i32`
  whose `v: i32`). The self-host historically ERASED generic structs
  (every `Box[…]` shared one shape; fields uniform 8-byte slots), so
  neither the monomorphiser nor the emitter tracked a generic struct's
  field types under instantiation. **Slice 8 adds generic-struct
  monomorphisation**, lifting that erasure and closing this boundary on
  all three backends.

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
  generic-struct `@derive` waited on generic-struct monomorphisation
  (now landed — slice 8). Both work for the cases the erasure model supports
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
  Then **INLINE variant-call receivers** landed (`Has(5).eq(…)`,
  `Nil.eq(…)`, `Circle(3).area()`): `struct_type_of` couldn't recover the
  enum type from a bare variant constructor (the variant→enum map is
  dropped at parse), so dispatch fell to the i32 path and compared
  pointers. `enum_of_variant` now reconstructs the map from the enum
  methods themselves — a receiver method whose type is an enum (not a
  struct) carries each variant in its `match (self)` arms — so an inline
  variant receiver dispatches to `$Enum__method` statically, exactly like
  the var-typed form. Finally **`dyn Trait`** landed (slice 9), closing the
  last wasm trait gap.

- **Self-host slice 8 (shipped): generic-struct monomorphisation.**
  Closes the slice-4 boundary: a generic struct instantiated at a
  primitive / string `T` (`Box[i32]`, `Box[string]`) is now cloned to a
  concrete struct (`Box__i32` whose `v: i32`, `Box__string` whose
  `v: string`) so a `@derive` / parametric-`impl` method body's
  `self.v.m()` resolves the field type statically. The pass
  (`parser.monomorphize_structs`, run inside `module_with_builtins` right
  after the function monomorphiser) reuses the existing
  `mono_infer` / `bind_unify` / `subst_ty` / `split_dunder` machinery:
  `StructDecl` now records its `type_params` (captured by
  `parse_struct_decl` instead of discarded); a walk rewrites every
  generic-struct annotation (`Box[i32]` → `Box__i32`, via `mg_ty`) and
  every struct literal (`Box { v: 5 }` → `Box__i32 { v: 5 }`, key inferred
  from the field values by `infer_lit_key`), collecting the
  instantiations; a worklist then clones each generic struct (fields
  substituted) and re-points its @derive-synthesised methods (bare `Box`
  receiver) and parametric-`impl` methods (`Box[T]` receiver + type
  params) at the mangled clone (`clone_struct_method`). The clones'
  literal display strings stay the ORIGINAL base name (`"Box { "` is baked
  at synth time), so `Display` output is unchanged. A no-op when no struct
  is generic — the self-host source + stdlib use none — which keeps the
  byte-identical fixpoint.

  The one cross-cutting emitter fix: `self` / struct-typed params were
  bound with `ret_tag_of(type)`, which maps a struct NAME to `"unknown"`,
  so `self.v` fell back to a first-match field scan across *all* structs —
  fine until two clones (`Box__i32`, `Box__string`) share a field name
  `v`, where the first-match returned the wrong clone's field type. Both
  native emitters now bind a receiver / param to its struct NAME when the
  declared type is a plain struct (the new `struct_local_tag` — bracketed
  types like `FuncDecl[]` / `Map[K, V]` / `Option[i32]` keep their coarse
  tag so arrays/maps/options aren't mis-typed), so `self.v` resolves
  `field_type_tag_in` the right struct. The wasm backend needed no emitter
  change — its static dispatch (`struct_type_of` → `$Box__i32__to_string`)
  routes the distinct clones for free. Tested via
  `trait-generic-struct-derive-{display-i32,display-string,display-both,eq,ord}`
  and `trait-generic-struct-parametric-impl` on x86-64 + arm64, the
  `generic-struct-*` cases through the wasm gate, and the x86-64 + arm64
  fixpoint gates (the compiler still builds itself byte-identically).

- **Self-host slice 9 (shipped): `dyn Trait` on wasm.** Closes the last
  wasm trait gap. The native backends dispatch a trait-object method by the
  value's runtime shape, so `dyn Trait` needed only the type parse there;
  the wasm backend is static-dispatch (`struct_type_of` → `$Type__method`),
  so a `dyn Shape` receiver — whose concrete type isn't known until run
  time — resolved to `""` and fell through to `(i32.const 0)`.
  `emit_dyn_dispatch` adds the missing runtime dispatch: it reads the
  receiver's struct id (the type tag at offset 0, the same one `match`
  reads) and branches to the matching `$Struct__method` over every struct
  that implements the method — the static-dispatch analogue of the native
  runtime shape-compare. It fires only as a fallback (after the
  struct-typed and primitive-receiver paths), so existing dispatch is
  untouched, and returns `""` (keeping the old fallback) when no struct
  implements the method. One companion fix: a `dyn Trait[]` local
  initialised with a struct-literal array (`var xs: dyn Named[] = [Cat {},
  Dog {}]`) was pinned to the first element's concrete type by
  `sa_elem_decl_type` (so the loop var dispatched statically to `Cat`);
  a `dyn`-spelled annotation now suppresses that homogeneous-array
  inference, keeping the loop var on the runtime path. `wasm.fern` is not
  in the native fixpoint bundle, so the fixpoint is unaffected. Tested via
  `dyn-object-{heterogeneous,param,string-method}` through the wasm gate.
  With this, **the full trait surface — traits/impls, parametric impls,
  `@derive` on structs + enums (incl. generic structs), enum methods, and
  `dyn Trait` — works on all three self-host backends.**

- **Self-host slice 10 (shipped): default trait methods.** Before this,
  `skip_trait_decl` consumed a `trait` block whole — the self-host had no
  trait method bodies to inherit, so a program relying on an inherited
  default failed to resolve the method. `parse_trait_decl` now replaces
  the skipper: it parses each trait method with the ordinary
  `parse_func_decl` machinery (a method with a non-empty body is a
  default; an abstract `;` signature comes back body-less and is dropped)
  and returns the defaults tagged with the simple trait name.
  `parse_impl_decl` retains the trait name + impl type + bounded type
  params and their bounds + the method names the impl provides, and the receiver-peel /
  `Self`→type / type-param-merge desugar it shared inline with default
  synthesis is factored into `finalize_impl_method`. After the whole
  module is parsed (so a trait declared *after* its impl still resolves),
  `parse_module` synthesises, for each impl, every default of its trait
  that the impl omitted — `finalize_impl_method(default, impl_type,
  impl_tps, impl_tbs)` — exactly the desugaring a written method gets.
  The bounds reach that call through `ImplInfo`, which is why they are
  recorded on the struct rather than kept as a local (#7224). Mirrors the
  Go checker's `synthesizeTraitDefaults`. **Same-module only**: a trait
  and an impl in *different* modules don't yet inherit (the synthesis runs
  per `parse_module`, before `merge_module`); cross-module defaults are a
  follow-up. Tested on x86-64 + wasm IR
  (`internal/e2e/self_host_default_method_ir_test.go`): inherited,
  overridden, default-calls-abstract, and two-impls-inherit-independently.

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
