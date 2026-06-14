# `dyn Trait` — runtime trait-object dispatch

Status: **design + slices 1–2 shipped** — interpreter (slice 1) plus all
three compiled backends (slice 2: wasm 2b, x86-64 2c, arm64 2d). This
document is the design of record; implement against it and update it as
slices land. It is the
runtime-dispatch counterpart to the static trait system in
[`TRAITS.md`](TRAITS.md) — read that first.

## 1. Motivation

The trait system today is **entirely static**. `impl Trait for Type`
desugars to receiver methods; `x.m()` is rewritten at compile time to
`__method_<Type>_m(x)`; bounded generics `[T: Trait]` are
**monomorphised** — one concrete clone per instantiation, dispatch
resolved by re-checking the clone. There is zero runtime dispatch and
zero per-call overhead. That is the right default for the language's
use cases (small fast-startup CLI tools, short-lived edge handlers).

What static dispatch *cannot* express is a **heterogeneous collection**:
a `Shape[]` whose elements are different concrete types — `Circle`,
`Rect`, `Triangle` — all behind one `Shape` interface, iterated and
dispatched without the call site knowing the concrete type. Today the
only way to model that is a closed `enum Shape { Circle(..), Rect(..) }`
+ `match`. That works and is often *better* (no allocation, exhaustive),
but it is **closed**: adding a variant edits the enum and every match.

`dyn Trait` is the **open** alternative: any type that `impl`s the trait
can be stored behind `dyn Trait` and dispatched at runtime, and a new
impl needs no edit to existing code. The cost is a runtime dispatch and
(for primitives) a heap box — paid only where `dyn` is written.

> **Guidance.** Prefer a closed `enum` + `match` when the set of types is
> known and small; that is the zero-cost, exhaustive choice and remains
> the idiom. Reach for `dyn Trait` only when the type set is genuinely
> open or the closed enum would be unwieldy. `dyn` is a tool, not the
> default — mirroring Rust's "enums first, `dyn` when you need it".

## 2. Surface syntax

`dyn Trait` is a type. `dyn` is a contextual keyword recognised only in
type position (so existing identifiers named `dyn` in value position are
unaffected — though we reserve it as a lexer keyword for simplicity, see
§6.1).

```fern
trait Shape {
    function area(self: Self): f64;
    function name(self: Self): string;
}

struct Circle { r: f64 }
struct Rect   { w: f64, h: f64 }
impl Shape for Circle { … }
impl Shape for Rect   { … }

function total_area(shapes: dyn Shape[]): f64 {
    var sum: f64 = 0.0;
    for s in shapes {
        sum = sum + s.area();   // runtime dispatch on each element
    }
    return sum;
}

function main(): i32 {
    var shapes: dyn Shape[] = [Circle { r: 1.0 }, Rect { w: 2.0, h: 3.0 }];
    //                          ^ Circle and Rect coerce to `dyn Shape`
    print(total_area(shapes).to_string());
    return 0;
}
```

- `dyn Trait` is a single nominal type for one trait (no `dyn A + B` in
  v1 — multi-trait objects are a follow-up; see §8).
- `dyn Trait` carries no postfix generics of its own; `dyn Trait[]`
  parses as `(dyn Trait)[]` — an array of trait objects.
- A `dyn Trait` value is produced by **coercion**: a concrete value
  whose type `impl`s `Trait` is implicitly boxed where a `dyn Trait` is
  expected (var init, assignment, argument, array element, `return`).
  No explicit cast syntax in v1.

## 3. Object safety

Not every trait can be a `dyn` object. A trait method is **object-safe**
iff, after the receiver `self: Self`, `Self` does not appear in any
parameter or in the result. The reason is mechanical: once a value is
behind `dyn Trait`, its concrete type is erased, so the compiler cannot
produce or type-check a *second* value of the same concrete type, nor a
return value whose type is the (now-erased) `Self`.

Rules (checked when a `dyn Trait` type is **used**, so a trait can have
non-object-safe methods and still be usable statically — only `dyn`
usage is gated):

1. Every method's first parameter is `self: Self` (already guaranteed by
   the trait grammar).
2. No method takes `Self` (or a type mentioning `Self`, e.g. `Self[]`,
   `Option[Self]`) in a non-receiver parameter.
3. No method returns `Self` (or a type mentioning `Self`).

A `dyn Trait` whose trait violates these is an error at the use site:

```
error[E0xx]: trait Eq is not object-safe: method `eq` takes Self as a
non-receiver parameter, so it cannot be used as `dyn Eq`
```

`Display` (`to_string(self): string`) is object-safe. `Eq`
(`eq(self, other: Self): boolean`) and `Ord` (`cmp(self, other: Self)`)
are **not** — they compare two values of the same erased type. This
matches Rust (`dyn Display` works; `dyn PartialEq` does not without
erasure tricks).

## 4. Dispatch model

### 4.1 Interpreter (slice 1 — this document)

The interpreter already tags every heap value with its concrete type:
`Struct{TypeName}`, `Enum{EnumName}`. So a `dyn Trait` value needs **no
new representation** — the boxed value *is* the concrete `Struct`/`Enum`
value, which already knows its type. Dispatch is by runtime tag:

- The checker type-checks `d.area()` (where `d: dyn Shape`) against the
  trait signature, and marks the `Call` as a **dynamic** method call
  (`Call.DynTrait = "Shape"`), leaving the callee as a `FieldAccess`
  (i.e. it does **not** rewrite to a concrete `__method_…`).
- `monomorph` leaves `DynTrait`-marked calls untouched (it can't pick a
  concrete clone — that's the point).
- The interpreter, evaluating a `Call` with `DynTrait` set: evaluate the
  receiver, recover its runtime type name (`Struct.TypeName` /
  `Enum.EnumName`), look up `__method_<TypeName>_<method>` in the
  function/builtin table, and call it with the receiver prepended. The
  orphan rule guarantees exactly one impl per (trait, type), so the
  lookup is unambiguous.

Primitives (`i32`, `string`, …) impl-ing a trait: in the interpreter
these are already `Value`s carrying enough tag info (`Number`, `String`)
to recover a type name, so boxing is a no-op there too. The
runtime-type-name helper maps each `Value` kind to the `methodTypeName`
key (`Number`→`"i32"`/`"i64"`/…, `String`→`"string"`, …).

### 4.2 Compiled backends (follow-up slices)

The arm64 / x86-64 / wasm backends monomorphise and carry **no** runtime
type metadata on structs, and do not box primitives. A `dyn Trait` there
needs a real runtime representation. The plan (not in slice 1):

- Represent a `dyn Trait` value as a **fat pointer**: `{ data, vtable }`
  — a heap pointer to the boxed concrete value plus a pointer to a
  per-(trait,type) **vtable** of method function pointers in trait-method
  declaration order. Coercion allocates the box (always, for primitives;
  for already-heap structs/enums the `data` is the existing pointer) and
  pairs it with the statically-known vtable for the concrete type.
- `d.m(args)` loads slot *k* (the method's index in the trait) from the
  vtable and `call`s it with `data` prepended.
- Vtables are static data emitted once per (trait, concrete-type) pair
  actually coerced in the program; a whole-program pass collects the
  coercion sites (the concrete types that flow into each `dyn Trait`).
- The runtime representation is **target-dependent**: wasm carries the
  fat pointer **inline** as two pointer-width slots (§4.2.1), while the
  natives **box** it as a single pointer to a `{data, vtable}` heap cell
  (§4.2.2). The IR layout helpers gate the two-word `DynTraitType` cases
  on `ptrW == 4`; on `ptrW == 8` a boxed `dyn` is an ordinary one-word
  pointer (`ast.IsPointerType` is true for it).

All three compiled backends now lower `DynTraitType` (wasm: inline
two-word, slice 2b; x86-64: boxed one-word, slice 2c; arm64: boxed
one-word, slice 2d), alongside the interpreter. The clean reject
diagnostic (`dyn Trait is not yet supported on compiled backends; …`)
remains the fallback for any future `ptrW == 8` backend that hasn't
opted in via `ir.DynSupported()`, so the gate never silently
miscompiles — but no shipping backend takes it today.

#### 4.2.1 Slice 2b implementation spec (wasm — next build)

Recon-derived, mechanical plan. **wasm goes first**: `ptrW==4` uniquely
identifies it (natives are `ptrW==8`), so its gate lifts in isolation,
and wasm already has the two-word inline value pathway (strings) to
mirror. Scope: dispatch correctness for **struct/enum** concrete types;
precise RC of the boxed object is a follow-up (the interp doesn't RC
trait objects either — a leak in a short-lived process is acceptable for
the first cut, tracked below).

Representation: inline two-word `[data, vtable]`, `data` low / `vtable`
high (mirrors `OpConstStr`'s `{data,len}` word order). `data` is the
concrete heap pointer; `vtable` is a static data-segment address holding
an array of `i32` function-**table indices** (positions in `prog.Funcs`),
one slot per non-associated trait method in declaration order.

1. **Checker** — record coercion sites. Add `Info.DynCoercions
   map[ast.Expr]DynCoercion{Trait,Concrete}`; a `maybeRecordDynCoercion(
   dst, *holder, srcType)` mirrors `maybeWrapForUnion`'s call sites (var
   init, assign, call arg, return, array elem, struct field) but
   *records* (doesn't rewrite) when `dst` is `DynTraitType`, `srcType` is
   a concrete struct/enum impl-ing the trait, and `srcType` isn't already
   `dyn`. Keyed by the holder `ast.Expr` pointer, which flows unchanged to
   the IR. (Limitation: a coercion inside a *generic* body is cloned by
   monomorph to a fresh pointer and missed — out of scope for 2b; assert
   non-generic in the test. Checker test: the right `{Trait,Concrete}` is
   recorded at a var-init site.)
2. **IR layout** — `payloadSlotSize`: `DynTraitType` → `2*ptrW`.
   `payloadStoreOpFor`/`payloadLoadOpFor`/`arrayElemStoreOpFor`: add a
   `DynTraitType` case returning `Width: WidthString` (the two-word
   fan-out is representation-agnostic — two adjacent i32s). Natives stay
   gated so their fall-through is unreached.
3. **IR ops** — `OpConstVtable{Trait,Concrete string}` → push the vtable
   address (i32); `OpCallDyn{Sig *ast.FuncType, I32 slot}` → dispatch.
4. **IR lowering** —
   - *Coercion*: when lowering an expr in `Info.DynCoercions`, lower the
     concrete value (one word = `data`), then emit `OpConstVtable{trait,
     concrete}` → stack `[data, vtable]`.
   - *Dispatch* (`Call.DynTrait != ""`): lower `fa.Target` → `[data,
     vtable]`; `OpStoreLocal(vtmp)` into a fresh **i32** temp pops just
     the top word (`vtable`), leaving `[data]`; lower args → `[data,
     args...]`; `OpLoadLocal(vtmp)` → `[data, args..., vtable]`; emit
     `OpCallDyn{Sig: receiver-first method sig, I32: slot}` where `slot`
     is the method's index in the trait decl (non-assoc methods only).
5. **Gate** — in `LowerWith`, call `rejectDynTrait` only when `ptrW != 4`.
   `collectVtables` already runs after the gate, so `prog.Vtables`
   populates for wasm automatically.
6. **wasm backend** (`internal/codegen/wasmbin/wasmbin.go`) —
   - Generalize `isStringType(slotType(...))` → an `isTwoWordType`
     (string ∨ `DynTraitType`) at: `OpLoadLocal`/`OpStoreLocal`
     (1643–1662), `slotValtypes` (1088), `resultValtypes` (1184), and the
     `OpLoad`/`OpStore` `WidthString` field-store branches (1978–2015).
   - `internVtable(trait, concrete)`: look up the `VtableDecl` in
     `prog.Vtables`, append `len(methods)` LE-`i32` `progFuncTableIdx[
     method.Func]` words to `dataBytes` at `stringNextOff`, return the
     address (no rc header — vtables are never inc/dec'd). `OpConstVtable`
     emits `i32.const` of that address.
   - `OpCallDyn`: stack `[data, args..., vtable]`; pop `vtable`→scratch,
     `+ slot*4`, `i32.load` → table-idx; `call_indirect` via
     `addSigType(op.Sig)` (the plain, **no-env** sig — *not*
     `addClosureSigType`).
   - `anyTableOp` must return true when `OpConstVtable`/`OpCallDyn` are
     present (and the impl methods are already in `prog.Funcs`, hence the
     table).
7. **Tests** — e2e: `var d: dyn Shape = Circle{...}; d.area()` on wasm
   matches the interp; a `dyn`-array differential (heterogeneous
   `dyn Shape[]`) vs interp. Keep the natives' reject test green.

**RC follow-up:** the `data` word's box currently leaks. Once dispatch is
solid, teach the RC passes to inc/dec word 0 of a two-word `dyn` slot and
skip word 1 (the static `vtable`). Tracked as part of goal 2 (Perceus).

#### 4.2.2 Slices 2c + 2d implementation (x86-64 + arm64 — BOXED one-word)

The natives (`ptrW==8`) deliberately do **not** reuse wasm's inline
two-word value pathway (they have none, and adding a two-word ABI on
natives is a large, separate change). Instead a native `dyn Trait` value
is **boxed one-word**: a single 8-byte heap pointer to a cell

```
{ data (8B @0), vtable (8B @8) }
```

where `data` is the concrete heap pointer and `vtable` is the absolute
address of the static `(trait, concrete)` vtable. This reuses the
existing one-word pointer pathway end-to-end — no two-word ABI changes
on natives. The vtable itself is an **array of function POINTERS**
(8-byte absolute addresses of the `__method_*` functions), *not* the
wasm-style table indices; `OpCallDyn` loads slot *k* and `call`s it
directly.

Representation-aware layout. The IR layout helpers
(`payloadSlotSize`, `payloadStoreOpFor`, `arrayElemStoreOpFor`,
`payloadLoadOpFor`, the `*ast.Index` load path, and
`ast.ElemSizeBytesFor`) gate their two-word `DynTraitType` cases on
`ptrW == 4`. On `ptrW == 8` a boxed `dyn` is just a pointer, so it falls
through to the existing `IsPointerType → WidthPtr` / `ptrW` branches —
one word everywhere on natives.

Target discriminator. `ptrW` alone cannot tell x86-64 from arm64
(both are 8). `ir.LowerWith` takes optional `LowerOption`s; **both**
native backends now pass `ir.DynSupported()` to lift their gate. The
gate is: reject `dyn` unless `ptrW == 4 || dynSupported`. The remaining
callers (the SSA / test harnesses) are behaviour-identical — they omit
`DynSupported()` and so still reject on `ptrW == 8`, which is what keeps
the fallback path exercised.

Lowering (boxed). The builder branches on `b.dynBoxed()` (= `ptrW
!= 4`):

- *Coercion*: after lowering the concrete value (`data`) and emitting
  `OpConstVtable` (pushes the vtable address), emit `OpBoxDyn` — a new IR
  op that pops `[data, vtable]`, allocates a `2*ptrW`-byte cell via the
  normal `__fern_alloc` path, stores `data` at +0 / `vtable` at +ptrW,
  and pushes the single cell pointer. `OpBoxDyn` is native-only; wasm
  never emits it (and its wasm handler is a guard that errors).
- *Dispatch*: the receiver lowers to a one-word cell pointer; stash it,
  push `data = load(cell + 0)`, lower args, push `vtable = load(cell +
  ptrW)`, then `OpCallDyn`. This reconstructs the SAME
  `[data, args..., vtable]` stack `OpCallDyn` already expects, so the op
  stays uniform across backends — only how `data`/`vtable` are obtained
  (deref cell vs inline) differs.

x86-64 backend (`internal/codegen/x86_64/x86_64.go`):

- `OpConstVtable` interns + materialises a `.rodata` cell
  (`__vtable_<trait>_<concrete>`) holding `len(methods)` 8-byte absolute
  `__method_*` pointers in trait-declaration order, then `lea`s its
  address (mirrors the `OpConstFunc` `.rodata` cell path).
- `OpBoxDyn` allocates 16 bytes via `__fern_alloc` and stores
  `{data, vtable}`.
- `OpCallDyn` pops the vtable, loads `[vtable + slot*8]` into `r11`,
  loads `[data, args...]` into the SysV arg registers (receiver-first,
  plain — no closure env), and `call r11`. Void iff `op.Sig.Result ==
  nil`.
- The vtable's `__method_*` targets are reachable only through the
  runtime vtable, so the build pins them as tree-shake roots via the
  shared `treeshake.DynCoercionImplMethods(info)` helper (hoisted out of
  the wasm build path so all backends share one rooting source).

arm64 backend (`internal/codegen/arm64/arm64.go`, slice 2d): the exact
structural mirror of x86-64 — same boxed representation, same IR (zero IR
changes), same vtable cells, just AArch64 instruction selection (AAPCS64).

- `OpConstVtable` materialises the `.rodata` `__vtable_<trait>_<concrete>`
  cell address via the `adrp` + `add :lo12:` PC-relative pair (the same
  pattern `OpConstFunc` / `OpConstStr` use), and interns the (trait,
  concrete) pair for emission.
- `OpBoxDyn` allocates 16 bytes via `__fern_alloc` and stores
  `{data, vtable}`. **Register choice differs from x86-64 here.** x86-64
  held `data`/`vtable` in `r10`/`r11` across the `call` because its
  `__fern_alloc` preserves them; arm64's `__fern_alloc` clobbers
  `x0..x14` (its bump/freelist body), so a caller-save register would not
  survive. The handler instead pops the two operands into caller-save
  `x9`/`x10` first (plain loads, no call in between), then saves the
  **callee-saved** `x19`/`x20` below the operand stack (`stp … [sp,
  #-16]!`, the same pair-save `emitMakeClosureOrEnv` uses), moves the
  operands into them, allocs, stores `{data, vtable}`, and restores
  `x19`/`x20`. Popping before the save is load-bearing: saving first
  would put the `x19`/`x20` pair on top of the operand stack and the
  subsequent pops would read the saved registers, not the operands.
- `OpCallDyn` pops the vtable into `x17`, loads `[x17 + slot*8]` into
  `x16` (the IP0/IP1 intra-procedure scratch pair `OpCallIndirect`
  already uses for its fn-pointer — `emitCallArgsLoad` only touches
  `x0..x7` plus `x9` for overflow copies, so `x16`/`x17` survive the arg
  load), loads `[data, args...]` into the AAPCS64 arg registers
  (receiver-first, plain — no closure env), and `blr x16`. The argument
  slot count is computed off `op.Sig` so a two-word-ABI string param
  occupies two operand-stack slots → two arg registers (the receiver is
  always one slot); a string result is pushed back as `(data, len)` from
  `(x0, x1)`. Void iff `op.Sig.Result == nil`.
- `.rodata` vtable cells emitted in `emitDataSections` (`.align 3` +
  `__vtable_<trait>_<concrete>` label + `.quad __method_*` per slot),
  with the Mach-O `__TEXT,__const` section choice on darwin.

**RC note (boxed).** The boxed cell *and* its `data` word currently leak
— same scope as wasm's slice 2b. The cell is a normal heap pointer now,
so the RC passes *could* try to inc/dec it, but `DynTraitType` has no
drop handler and the lowering emits no inc/alias on a `dyn` value, so no
RC traffic is generated for it. Precise RC of the boxed cell (and its
inner `data`) is the same Perceus follow-up tracked above.

### 4.3 Self-host (x86-64 + arm64 — shipped)

The self-hosted compiler dispatches heap values dynamically by shape
pointer already, so `dyn Trait` maps onto that path for free — a
struct/enum value carries its own shape, and `d.m()` shape-dispatches to
the concrete impl regardless of `d`'s static type. So the self-host
needed only the **parse**, not a new dispatch path:

- `dyn` is a lexer keyword (`is_keyword`), and `parse_type_name`
  consumes `dyn Trait` into a coarse `"dyn <trait>"` spelling (recursing
  so `dyn Shape[]` / `dyn mod.Shape` reuse the array/qualified handling).
- One real fix in the x86-64 + arm64 emitters: `ret_tag_of` collapses
  every non-scalar `T[]` to the generic `"array"` tag (the element name
  is lost), and the `for x in xs` lowering defaulted that element to
  `i32` — so a method call on the loop var (`for x in shapes {
  x.area() }`) mis-dispatched to the primitive path. The generic
  `"array"` tag now binds the loop var as `"unknown"`, which routes the
  call through runtime-shape dispatch. (This was a pre-existing bug for
  *any* struct array, not just `dyn` — `for p in points { p.m() }` hit
  it too; the `dyn` work surfaced it.)

Same boundary as elsewhere: `dyn` over a **struct/enum** concrete type
works (it has a shape); over an unboxed **primitive / string** it does
not (no shape pointer) — that needs the monomorphisation path. The
self-host checker (`checker.fern`) does not yet enforce object-safety or
the coercion rule; the Go checker is the strict gate until it retires,
at which point those rules move into `checker.fern`. The **wasm**
self-host backend (static-dispatch, no runtime shape-compare) now handles
`dyn Trait` via `emit_dyn_dispatch`: it reads the receiver's struct id
(the offset-0 type tag) and branches to the matching `$Struct__method`
over every implementing struct — see TRAITS.md §7a slice 9.

## 5. Coercion (boxing) model

A concrete value coerces to `dyn Trait` exactly where a `dyn Trait` type
is expected and the value's concrete type `impl`s the trait:

- `var d: dyn Shape = Circle { … };`
- assignment `d = Rect { … };`
- argument passing to a `dyn Shape` parameter
- array element `[Circle{…}, Rect{…}]` against `dyn Shape[]`
- `return circle;` from a `dyn Shape`-returning function

The single gate is the checker's assignability relation (`assignable`):
`dst = dyn Trait`, `src = C` ⊢ ok iff `Info.Impls[Trait][methodTypeName(C)]`.
Because `assignable` is consulted at every one of these sites, threading
the impl check through it covers all coercion points uniformly. A
`dyn Trait` is **not** assignable back to a concrete type (no downcast in
v1) and two different `dyn` types do not inter-assign.

## 6. Implementation map

### 6.1 Lexer (`internal/lexer/lexer.go`)
Add `dyn` to the keyword set. (Keyword rather than contextual to keep the
parser simple; `dyn` was not previously an identifier anywhere in the
stdlib or examples.)

### 6.2 AST (`internal/ast/ast.go`)
- `DynTraitType{Trait string}` implementing `isType()` + `String()`
  (`"dyn Shape"`).
- `ast.Equal`: two `DynTraitType` are equal iff same `Trait`.
- `ast.IsPointerType`: `true` (pointer-shaped; two-word on compiled
  backends, single tagged value in interp).
- `ast.SubstSelf`: passes through (a `dyn` type contains no `Self`).
- `Call.DynTrait string`: set by the checker to mark a dynamic method
  call; empty for ordinary calls.

### 6.3 Parser (`internal/parser/parser.go`)
In `parseType`, a leading `dyn` keyword → parse a trait name →
`DynTraitType{Trait}`. Postfix `[]` / `[…]` continues to apply to the
whole `dyn Trait` (array-of). No bounds, no generic args on the trait.

### 6.4 Checker (`internal/checker/checker.go`)
- **Type validity**: a `DynTraitType{Trait}` is valid iff `Trait` is a
  known trait; otherwise `E0xx unknown trait in dyn type`. Check
  object-safety (§3) at the same point; cache the per-trait result.
- **Coercion**: extend `assignable` (made impl-aware — pass the checker's
  `Info.Impls`, or convert the key call sites to a `c.assignable`
  method) so `C → dyn Trait` succeeds when `C` impls `Trait`.
- **Dynamic dispatch**: in the `*ast.Call`/FieldAccess path, when the
  receiver's static type is `DynTraitType{Trait}`: resolve the method in
  the trait's method list (not the concrete method table); type-check
  arguments against the trait signature (with `Self`→ the receiver's
  `dyn` type for the receiver slot only — other `Self` positions are
  already rejected by object-safety); set `Call.DynTrait = Trait`; do
  **not** rewrite the callee. Result type is the trait method's result.
- **Method-set restriction**: only trait methods are callable on a
  `dyn Trait`; field access and non-trait methods are errors.

### 6.5 Monomorph (`internal/monomorph/monomorph.go`)
Skip rewriting/cloning for `Call`s with `DynTrait != ""` — they stay
dynamic. (`DynTraitType` carries no type parameters, so nothing to
monomorphise.)

### 6.6 Interpreter (`internal/interp/interp.go`)
- A runtime-type-name helper `valueTypeName(Value) (string, bool)`
  mapping each `Value` kind to its `methodTypeName` key.
- In `evalCall`, when `c.DynTrait != ""` and the callee is a
  `FieldAccess`: evaluate the receiver, recover its type name, look up
  `__method_<name>_<field>` in `Builtins`/`Funcs`, call with the
  receiver prepended.
- No new `Value` kind needed (the boxed value is its concrete value).

### 6.7 Codegen gating (arm64 / x86-64 / wasm IR lowering)
Emit a clean unsupported-feature error on encountering `DynTraitType`
(or a `DynTrait` call) until the vtable slices land.

## 7. Phasing

1. **Slice 1 (this PR): surface + interpreter.** Lexer + AST + parser +
   checker (validity, object-safety, coercion, dynamic dispatch) +
   monomorph skip + interpreter runtime dispatch + compiled-backend
   gating. Heterogeneous `dyn Trait[]` works end-to-end on the
   interpreter. Tests at every layer.
2. **Slice 2: compiled-backend vtables.** Fat-pointer representation +
   per-(trait,type) vtable emission + coercion boxing + indirect-call
   lowering, delivered foundation-first then one backend per PR:
   - **2a (landed): vtable-collection scaffolding.** `ir.VtableDecl` /
     `VtableMethod` + `Program.Vtables`, and `collectVtables` — one table
     per (trait, concrete-type) where the trait is used in a `dyn` type
     and the type implements it, slots in trait declaration order. Wired
     into `LowerWith` (nil today: the reject gate still returns first for
     `dyn` programs). Unit-tested in `internal/ir/vtable_test.go`. No
     behaviour change; it's the static data every backend will emit.
   - **2b (landed): wasm codegen** — full implementation spec in §4.2.1.
     wasm leads (its `ptrW==4` gate lifts in isolation; it already has the
     two-word inline pathway). Inline `[data,vtable]`, checker-recorded
     coercion sites, `OpConstVtable` + `OpCallDyn`, vtable data segments.
   - **2c (landed): x86-64 codegen** — BOXED one-word representation
     (§4.2.2). A `dyn Trait` is a single heap pointer to a `{data,
     vtable}` cell; the vtable is an array of absolute `__method_*`
     function pointers. Reuses the existing one-word pointer pathway (no
     native two-word ABI). Adds `OpBoxDyn`, a `LowerWith` target
     discriminator (`ir.DynSupported()`), and the x86-64 `OpConstVtable`
     / `OpCallDyn` / `OpBoxDyn` handlers + `.rodata` vtable cells.
   - **2d (landed): arm64 codegen** — the SAME boxed one-word
     representation as x86-64 (it is also `ptrW==8`), the structural
     mirror with zero IR changes (§4.2.2). arm64 passes
     `ir.DynSupported()` to lift its gate and gains the mirrored
     `OpConstVtable` / `OpBoxDyn` / `OpCallDyn` handlers + `.rodata`
     vtable cells. The one divergence from x86-64 is register lifetime:
     arm64's `__fern_alloc` clobbers `x0..x14`, so `OpBoxDyn` parks
     `data`/`vtable` in the callee-saved `x19`/`x20` across the alloc
     (x86-64 used caller-save `r10`/`r11`, which its alloc preserves),
     and `OpCallDyn` holds the fn pointer in `x16`/`x17`. **All three
     compiled backends now support `dyn` — no compiled backend rejects
     it.**
3. **Slice 3: self-host parity (x86-64 + arm64 — shipped).** `dyn Trait`
   parses in the self-host and dispatches over its existing shape-pointer
   path; struct/enum concrete types work end-to-end (see §4.3). Remaining:
   the wasm self-host backend, and the strict object-safety / coercion
   checks in `checker.fern` (only needed once the Go checker retires).
4. **Follow-ups.** Multi-trait objects (`dyn A + B`), explicit upcast/
   downcast, `dyn` in struct fields with the fat-pointer layout.

## 8. Testing

Per the engineering bar (tests at the layer each change touches):
- **Parser** (`parser_test.go`): `dyn Trait` and `dyn Trait[]` parse to
  `DynTraitType`; `dyn` in array/param/return position.
- **Checker** (`checker_test.go`): unknown-trait `dyn` rejected;
  object-safety rejection (`dyn Eq`); coercion accepted for an impl-ing
  type and rejected for a non-impl-ing type; non-trait method on a `dyn`
  rejected; result type of a `dyn` method call.
- **e2e** (`internal/e2e`): a heterogeneous `dyn Shape[]` with two
  concrete impls, iterated + dispatched on the interpreter, printing the
  expected per-element results; the compiled-backend gating diagnostic.
- Full suite (incl. WASM e2e + self-host gates) stays green; the
  interpreter-only scope means no differential (interp-vs-compiled) test
  exercises `dyn` until slice 2.
