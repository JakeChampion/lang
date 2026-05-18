# Feature gaps surfaced by bug-hunt probes

A bug-hunt session (May 2026, PRs #562–#580) systematically probed the
language for ill-handled corner cases. Most surfaced were real bugs and
got fixed; the entries below are **missing features** the probes hit
that need separate work to land.

Each entry has: the surface syntax that failed, the rough complexity
of adding it, and a sketch of where the fix lands.

---

## 1. Anonymous function expressions (lambdas)

**Probe:**
```lang
var f: (i32) => i32 = function (x: i32): i32 { return x; };
```
**Error:** `expected ";", got "function"` at the `function` keyword
on the RHS of `var`.

**Today:** `function` is only valid as a top-level / nested-stmt
keyword. A function value can be produced only by declaring a named
nested function and returning its name.

**Lift:**
- Parser: accept `function (params): R { body }` as an expression
  form (no name, no `IsLocal` stmt-decl shape — emits a synthetic
  named FuncDecl + the same MakeClosure path closureconv already
  uses for named nested functions).
- Checker: capture analysis stays identical — anonymous fn body
  walks the same `captureSink` path.
- Suggested name in fresh-counter form: `__lambda_<N>`.

**Estimate:** Medium. The hard part is parser disambiguation
(`function`-as-expression vs `function`-as-stmt). The semantics
piggyback on the existing closure infra.

---

## 2. Parenthesized function types in array / slice declarations

**Probe:**
```lang
var arr: ((i32) => i32)[] = [];
```
**Error:** `expected "=>" after parameter list (function type) or
2+ comma-separated types (tuple type)`.

**Today:** the parser looks for `=>` immediately after `(...)`,
which the inner parens of a function-type-in-array decl break.
Tuples and function types share the `(...)` prefix; the disambiguator
peeks for `=>` after the close-paren.

**Lift:**
- Parser: extend the type parser to accept `((T1, T2, ...) => R)[]`
  by recognising parens that wrap a complete function type as a
  "type group" (analogous to expression-grouping parens). The
  ambient `()` already exists for tuple element separation; the
  parser needs to look past the close-paren for a `[]` / `[T]`
  suffix and back-track.

**Estimate:** Small-to-medium. Pure parser change, no IR / runtime.

---

## 3. `else if` as expression

**Probe:**
```lang
var sign: i32 = if (n > 0) { 1 } else if (n < 0) { 0 - 1 } else { 0 };
```
**Error:** `expected "{", got "if"` at the `if` after `else`.

**Today:** `else` in an *expression* IfExpr only accepts a `{...}`
block. `else if` is parsed at the *statement* level but not at the
expression level.

**Lift:**
- Parser: after `else` in an IfExpr, peek for `if` and recurse into
  IfExpr parsing — same shape as the existing stmt-level `else if`
  handling. The recursive IfExpr's body is the implicit "{...}"
  the AST already expects.

**Estimate:** Small. Targeted parser change. No checker / IR / codegen
changes; the recursive IfExpr already has correct type-unification.

---

## 4. Generic call-site type arguments

**Probe:**
```lang
function makeId[T](): (T) => T { ... }
var f = makeId[i32]();
```
**Error:** `unexpected token "i32"` at the `[i32]`.

**Today:** generic instantiation is fully type-inferred from the
call's arguments and the expected return type. Calls with no
arguments (or where the return type can't be inferred from the
context) have no way to pin down `T`.

**Lift:**
- Parser: accept optional `[T1, T2, ...]` between callee and `(...)`.
  Stamp the type args on `*ast.Call.TypeArgs` (already present for
  some uses — Map methods).
- Checker: when `TypeArgs` is set, treat it as the inferred
  substitution map instead of running argument-side inference.

**Estimate:** Medium. The checker already plumbs `TypeArgs`
through generic dispatch (used by Map's K/V), so the addition is
recognising the source-level syntax for it.

---

## 5. Mutual recursion of local functions

**Probe:**
```lang
function main(): i32 {
    function isEven(n: i32): boolean {
        if (n == 0) { return true; }
        return isOdd(n - 1);   // ← `isOdd` not yet bound at this point
    }
    function isOdd(n: i32): boolean {
        if (n == 0) { return false; }
        return isEven(n - 1);
    }
    if (isEven(10)) { return 1; }
    return 0;
}
```
**Error:** `undefined identifier "isOdd"` inside `isEven`'s body.

**Today:** the checker visits local FuncDecls in source order
(`checkBlock` walks statements). A nested decl's name is only bound
in `outer.names` after its own `checkLocalFunc` call begins —
forward references to a sibling nested function aren't yet visible.

Top-level mutual recursion works because all top-level functions
are registered in `info.FuncSigs` in a pre-pass before any body is
checked.

**Lift (initial attempt — REVERTED, deeper than it looked):**
A checker pre-pass that binds every local FuncDecl's name +
signature in the block's scope before walking statements DOES
make type-checking succeed. But the runtime is broken:
closureconv produces `var isEven = MakeClosure{__closure_isEven_1,
[isOdd]}` followed by `var isOdd = MakeClosure{__closure_isOdd_2,
[isEven]}`. At runtime the captures expressions run in source
order — when isEven's MakeClosure evaluates its `[isOdd]` capture
slot, isOdd is still uninitialised (zero). isEven's env ends up
with a stale capture; when isEven's body calls isOdd through the
env, it dispatches to table index 0 (which happens to be isEven
itself). The infinite recursion eventually hits n=0 and returns
— so simple `isEven(10)` accidentally produces the right answer,
but `isEven(11)` (or `isOdd(8)`) returns the WRONG answer
silently. A user staring at "1 = true" would believe their code
is correct.

**Real lift:**
- Same checker pre-pass — but ALSO emit a closureconv shape that
  works under cyclic captures. Options:
  1. Pre-allocate every sibling's env block first (zero-filled),
     build all the MakeClosure pairs (which point into the env
     blocks), then go back and store each closure's capture
     values into its env block. The cycle now closes because
     every closure pair pointer exists before any env's stores
     run.
  2. Detect "every sibling local FuncDecl in this block has
     identical mutual-reference structure" and bypass the env
     block for the sibling-references — emit direct calls to
     the hoisted top-level names instead. Limited but enough
     for the isEven/isOdd shape.
- The hoist order in closureconv already gives each function its
  table index up front; the missing piece is the env-block init
  scheme.

**Estimate:** Medium-large. The checker side is small but the
closureconv + IR changes are nontrivial. Marked DEFERRED for now.

---

## 6. Match on literal patterns (bool / integer / string)

**Probe:**
```lang
match (n) {
    0 => "zero",
    1 => "one",
    _ => "many",
}
```
**Error:** `expected variant pattern or '_' in match arm, got 0`.

**Today:** match arms accept only `VariantName(bindings)` or `_`.
`switch (n) { case 0: …, case 1: …, default: … }` is the supported
form for integer dispatch.

**Lift:**
- Parser: accept literal patterns (NumberLit / StringLit / BoolLit)
  in match-arm position alongside variant patterns.
- Checker: type-check the literal against the scrutinee's type;
  collect coverage for exhaustiveness against the same wildcard
  fall-through that variant patterns use.
- IR: lower a literal-pattern match arm to an equality test +
  branch (same shape `switch` already produces internally).

**Estimate:** Medium. New AST kind for the literal-pattern arm, but
each layer's change is targeted.

---

## 7. Tuple destructuring in `var` bindings

**Probe:**
```lang
var (a, b) = getTuple();
```
**Error:** `expected "Ident", got "("` at the `(`.

**Today:** `Destructure` AST node exists for `[a, b, c]` array
destructuring; tuple destructuring uses the same kind but the
parser doesn't accept the `(name, name)` form.

**Lift:**
- Parser: when seeing `var (`, parse a comma-list of identifiers
  and emit a `Destructure` AST node tagged as tuple-shape (the
  existing kind already takes a Names slice).
- Checker: type the RHS as TupleType and pair each Names[i] with
  Elems[i].
- IR: lower as N OpStoreLocal stmts, one per element, like array
  destructuring already does.

**Estimate:** Small. The existing `Destructure` infra covers
the IR side; this is mostly parser + a checker dispatch.

---

## 8. Generic struct method with type parameters

**Probe:**
```lang
struct Box[T] { value: T }
pub function [T] (b: Box[T]) unwrap(): T { return b.value; }
```
**Error:** `expected "Ident", got "["` at the `[T]` before the
receiver.

**Today:** methods on generic structs must instantiate the type
param concretely (`pub function (b: Box[i32]) get(): i32 { … }`),
forcing one method body per instantiation.

**Lift:**
- Parser: accept `[T1, T2, ...]` between `function` and the
  receiver-param list.
- Checker: thread the method's type params through `Box[T]`'s
  resolution so the receiver type unifies with the method's own
  parameter list. Monomorphisation: emit one method body per
  `Box[T]` concrete instantiation reached from a call site.

**Estimate:** Larger. The monomorphisation path already exists for
generic functions; extending to methods needs the bridge between
the method's type params and the struct's.

---

## Stretch ideas (out of scope for first pass)

- **Variadic function args** (rest-syntax `...args`)
- **String interpolation with format spec** (`f"{n:04}"`)
- **First-class methods as function values** (`b.get`, evaluating to
  a bound `() => T`)
- **Async / channel / coroutine primitives**
- **Try-block / try-with for Result that isn't `?`-shaped**

---

## Priority order for first batch

Driven by "smallest, no-runtime-touch, unlocks the most user code":

1. ✅ **Tuple destructuring (#7)** — landed in PR #581.
2. ✅ **`else if` as expression (#3)** — landed in PR #581.
3. ✅ **Parenthesized function types in arrays (#2)** — landed in
   PR #581 + IR's `call()` handles `*ast.Index` callees.
4. **Mutual recursion of local fns (#5)** — DEFERRED. Naive
   checker pre-pass produces silently-buggy runtime captures
   (siblings see uninitialised env slots). Needs a real fix
   in closureconv (pre-alloc envs + back-fill). See entry #5
   above.
5. ✅ **Anonymous lambdas (#1)** — landed in PR #584.
6. ✅ **Match on literal patterns (#6)** — landed in this PR.
7. ✅ **Generic call-site type args (#4)** — landed in this PR.
   Disambiguated from `arr[i]` by requiring a leading type
   keyword inside the brackets.
8. **Generic struct method type params (#8)** — biggest; needs
   monomorph reach into methods.
