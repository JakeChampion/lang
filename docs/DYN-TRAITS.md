# `dyn Trait` — runtime trait-object dispatch

Status: **design + slices 1–4 shipped** — interpreter (slice 1), all
three compiled backends (slice 2: wasm 2b, x86-64 2c, arm64 2d), the
self-host (slice 3: x86-64 + arm64 + wasm), and Perceus RC for trait
objects on **all three backends** (slice 4: wasm 4a, x86-64 4b, arm64 4c,
§4.4), now including `dyn` values held as **array elements**
(`dyn Trait[]`, all backends), as **enum payloads / struct fields / tuple
elements** (`enum Box { Wrap(dyn Shape) }`, x86-64 + arm64), and **captured
by a closure** (x86-64 + arm64). Dispatch is complete
everywhere; standalone + array `dyn` reclaim on every backend; enum-payload
/ struct-field / tuple-element and closure-captured `dyn` reclaim on the
natives (wasm keeps these leaking — its inline two-word `dyn` double-drops a
matched-and-bound payload, and a captured copy isn't reclaimed). The
closure-capture fix also closed a **use-after-free**: an escaping closure's
captured `dyn` was both swept (incorrectly, via the source local) and held
by the returned env — see §7.8. Still flagged-leaking: **map value** `dyn`
(headerless boxed cell can't join the map's rc-header value-reclamation —
§7.8) and a `dyn Trait[]` array captured by a closure; see §7.8. This document is the design of record; implement against it and update it
as slices land. It is the
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

- `dyn Trait` is a trait-object type. Multi-trait objects `dyn A + B`
  (interpreter + all three compiled backends + the self-host, via the
  merged vtable — dispatch AND `as?` downcast) span a set of traits —
  see §10.
- A **generic** trait is used as a `dyn` object by **pinning** its type
  parameter(s): `dyn Container[i32]` (mirrors Rust's `dyn Container<i32>`).
  The arguments are erased at runtime — the vtable is still keyed by trait
  name — so they drive only the checker's coercion gate (a concrete type
  coerces iff it `impl`s the trait at *those* arguments:
  `impl Container[i32] for BoxI` ⇒ `dyn Container[i32]`, not
  `dyn Container[string]`) and the method-signature substitution that makes
  `get(self: Self): T` read as `get(): i32`. A generic trait used as `dyn`
  *must* pin all its parameters — bare `dyn Container` is an error (the
  erased `T` in a method signature would be unresolvable at the call site).
  See §2.1.
- The pinned-args `[…]` (one or more types) is distinct from the array
  suffix `dyn Trait[]` (empty brackets), which parses as `(dyn Trait)[]` —
  an array of trait objects. `dyn Container[i32][]` is an array of
  `dyn Container[i32]`.
- A `dyn Trait` value is produced by **coercion**: a concrete value
  whose type `impl`s `Trait` is implicitly boxed where a `dyn Trait` is
  expected (var init, assignment, argument, array element, `return`).
  No explicit cast syntax in v1.

### 2.1 Generic trait objects — `dyn Container[i32]`

```fern
trait Container[T] {
    function get(self: Self): T;
}
struct BoxI { v: i32 }
struct Pair { a: i32, b: i32 }
impl Container[i32] for BoxI { function get(self: Self): i32 { return self.v; } }
impl Container[i32] for Pair { function get(self: Self): i32 { return self.a + self.b; } }

function sum(d: dyn Container[i32]): i32 {
    return d.get();   // dispatched by concrete type; statically typed i32
}

function main(): i32 {
    var x: dyn Container[i32] = BoxI { v: 40 };
    var y: dyn Container[i32] = Pair { a: 1, b: 1 };
    return sum(x) + sum(y);   // 42
}
```

Representation and dispatch are exactly as for a non-generic `dyn Trait`
(§4): the trait argument is **erased**, so there is no per-instantiation
vtable — `impl Container[i32] for BoxI` registers the same
`__method_BoxI_get` the boxed object's vtable points at. The argument list
lives only in the `ast.DynTraitType.Args` carried alongside `Traits`
(parallel slices, normalised together by `ast.NewDynTraitTypeArgs`), and is
consulted by the checker at two points:

- **Coercion gate** (`implementsAllDynTraits`): the concrete type's recorded
  impl arguments (`Info.ImplTraitArgs[trait][type]`) must equal the pinned
  arguments. A `Container[string]` impl does not satisfy `dyn Container[i32]`.
- **Method resolution** (`checkDynMethodCall`): the owner trait's type
  parameters are substituted with the pinned arguments before the signature
  is read, so a method returning the trait param `T` types as the pinned
  type. `Self` is still substituted to the `dyn` type itself.

### 2.2 Associated-type objects — `dyn Producer[Item = i32]`

A trait with **associated types** is object-unsafe *unless* the `dyn` type
**pins** every one — `dyn Producer[Item = i32]` (mirrors Rust's
`dyn Iterator<Item = i32>`):

```fern
trait Producer {
    type Item;
    function get(self: Self): Self::Item;
}
struct IntBox { v: i32 }
impl Producer for IntBox { type Item = i32; function get(self: Self): i32 { return self.v; } }

function sum(d: dyn Producer[Item = i32]): i32 {
    return d.get();   // Self::Item reads as i32
}
```

The pin uses the same `[…]` bracket as positional generic arguments — a
`Name = Type` entry is an associated-type binding, a bare `Type` a positional
argument, and both may appear together (`dyn Foo[i32, Item = T]`). It is carried
in `ast.DynTraitType.AssocBindings` (parallel to `Traits`, sorted by name), and
drives three checker points:

- **Object safety** (`objectSafe`): every associated type the trait declares
  must be pinned, else the trait stays object-unsafe (the diagnostic names the
  unpinned one and the `[Name = …]` fix).
- **Coercion gate** (`implementsAllDynTraits`): the concrete type's impl
  binding (`Info.AssocBindings`) must equal the pin — `IntBox` with
  `type Item = i32` coerces to `dyn Producer[Item = i32]`, not
  `dyn Producer[Item = string]`.
- **`Self::Item` projection**: resolved to the pinned type in method signatures
  (`checkDynMethodCall` for type-checking; `ir.dynSigResolver` for the
  `OpCallDyn` signature, so the wasm seam sees a concrete type — not a bare
  `ProjType`).

As with generic arguments the binding is erased at runtime.

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

1. No method takes `Self` (or a type mentioning `Self`, e.g. `Self[]`,
   `Option[Self]`) in a non-receiver parameter.
2. No method returns `Self` (or a type mentioning `Self`).

A trait requirement with no `self` receiver is an **associated function**
(`trait Zero { function zero(): Self; }`). Whether such a trait may be a
`dyn` object at all is open (#7264) — native's `objectSafe` allows it and
the self-host's does not. What is settled either way is the CALL: an
associated function has no receiver to dispatch on and gets no vtable
slot, so `d.f(...)` through a `dyn` value is E021 in both compilers
(#7398). Reach it through a concrete type instead.

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
skip word 1 (the static `vtable`). Designed in **§4.4** (goal 2, Perceus).

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
inner `data`) is designed in **§4.4** (the Perceus follow-up): a trailing
vtable drop slot + a per-trait `__drop_dyn_<Trait>` that runs the concrete
destructor and frees the cell.

**Register lifetime across `__fern_alloc` (boxed coercion).** `OpBoxDyn`
holds `data`/`vtable` across `call __fern_alloc`, whose heap-init /
heap-grow path does an mmap `syscall` — which clobbers caller-save regs
(on x86-64 the CPU stashes RFLAGS into `r11` and the mmap arg lives in
`r10`; arm64's `__fern_alloc` clobbers `x0..x14`). So both natives park
`data`/`vtable` in **callee-saved** regs across the call (x86-64:
`rbx`/`r12`; arm64: `x19`/`x20`), popping both operands *before* the
save so the saved pair never aliases the operand-stack slots. This is
load-bearing exactly when the box is the program's **first** allocation
— e.g. a `dyn` over a primitive, whose value isn't separately
heap-allocated — since that box alloc is the one that triggers heap
init. (The struct cases never hit it: the struct is allocated, and the
heap initialised, before its box.)

#### 4.2.3 `dyn` over primitive receivers — uniform boxing

A primitive/`string` value can `impl` a trait and coerce to `dyn` (the
checker allows it; the interpreter handles it by tag). Holding the value
directly in the `data` word only worked while it fit one slot and used
the integer-register receiver ABI, which excluded `i64`/`f64` on wasm and
`string` everywhere but x86-64. **Uniform boxing** removed that
restriction: at a coercion site `boxPrimitiveDynValue` heap-boxes the
value into a cell sized by the concrete's own layout, so `data` is always
a one-word pointer, and the vtable method slot points at a synthesized
unboxing wrapper `__dynbox_<C>_<m>` (`load value; call
__method_<C>_<m>`), built by `buildDynboxWrappers`. Every primitive
receiver — `i32`, `boolean`, `u8`, `i64`, `f64`, `string` — dispatches on
all three backends, pinned by `Test*DynTraitPrimitive*`.

Two constraints follow from the wrapper being generated code rather than
static data, both of which a STDLIB trait is the first thing to hit
(`TestDynTraitStdlibDisplay`), because its implementor set is written by
the stdlib rather than by the program under test:

- **Every implementor's concrete name needs a value-box layout.**
  `astTypeForConcreteName` has to cover every spelling
  `ast.ReceiverTypeName` can produce, not just the ones a program is
  likely to coerce — one missing entry rejects the whole `dyn` set at
  lowering. `u8` was the gap, and `core/cmp` declares `impl Display for
  u8` (the byte type has no scalar module, so its impl carries a real
  body), which made `dyn cmp.Display` unlowerable on every backend.
- **A wrapper is only built for a concrete some site COERCES.**
  `collectVtables` deliberately over-approximates, emitting a vtable per
  implementor; an unreferenced vtable is dead static data no backend
  emits, but a wrapper is dead CODE calling `__method_<C>_<m>` — a symbol
  tree-shake dropped, since `treeshake.DynCoercionImplMethods` roots only
  the coerced concretes. `buildDynboxWrappers` therefore filters on
  `info.DynCoercions`. That is complete: a primitive vtable can only be
  materialised by a coercion, since an `as?` target is always a
  struct/enum.

### 4.3 Self-host (x86-64 + arm64 — shipped)

The self-hosted compiler dispatches heap values dynamically by shape
pointer already, so `dyn Trait` maps onto that path for free — a
struct/enum value carries its own shape, and `d.m()` shape-dispatches to
the concrete impl regardless of `d`'s static type. So the self-host
needed only the **parse**, not a new dispatch path — with one exception
below:

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

**The exception — the trait set rides on the op (#6931).** Shape dispatch
alone is enough only while a `(Type, method)` pair has ONE definition.
Since two different traits may each provide a method of that name for one
type, the self-host emits the second provider as `<Type>.<Trait>.<m>`
(`parser.claim_method_name`) so the symbol namespace stays injective — and
a dispatch chain searching for a bare `m` would then find only the FIRST
trait's provider, whatever `d`'s trait says. So `op_dyn_dispatch` carries
the dyn type's trait set alongside the method name (`str` is `m|B` /
`m|A,B`), and every backend's arm enumerator resolves through
`irlower.dyn_arm_matches`: a receiver whose provider for one of the dyn's
traits was interposed matches THAT definition, and its bare namesake — a
different trait's method — does not answer for it. Receivers with no
collision keep matching the bare name, so `dyn A + B` where A provides `m`
and B provides `n` is unaffected. The reading of the claim table itself is
`parser.dyn_arm_matches` / `parser.dyn_provider_name`, beside the renaming
it undoes, so both dispatch models share one rule.

The self-host's AST **interpreter** (`interp.fern`) makes the same
resolution without a type checker (#6984). Its `Env` carries each binding's
DECLARED type spelling parallel to the value — the slot the checker's Scope
holds a `Type` in — and a method call resolves its written name through
`parser.dyn_provider_name` against that. The shapes a declaration is in
reach of are covered: a `var` binding, a parameter, a closure capture, and
an element of an annotated array (indexed or iterated). A receiver whose
static type has no declaration in reach — a struct field, a call result, an
inferred binding — answers on the runtime value alone, i.e. the first
provider, since the interpreter has no type inference to ask.

Primitive / `string` receivers behind `dyn` now work in the self-host
too, via the same **uniform boxing** as native (§4.2.3) adapted to the
shape-pointer model: at a coercion site a primitive/string value is
heap-boxed into a cell `[shape@0, value@8]` (the `op_dyn_box` op), where
`shape` is the concrete's interned type-name (the same id struct shapes
use) and `value` is the one-word scalar (or a string's box pointer). Each
backend's `dyn_dispatch` chain gained primitive-receiver arms that match
the box's offset-0 shape and **unbox** offset 8 as the receiver. Wired at
the two reachable, fixpoint-safe coercion sites — **scalar `dyn` call
args** and **`dyn Trait[]` array-literal elements** (the §4.2 motivating
shapes: passing to a function + heterogeneous collections), detected via
a `'2'` flag in the existing `fn_param_sigs` registry (no new
`LowerState` field, so the byte-identical bootstrap is untouched).
**Remaining (next self-host increment):** the scalar `var d: dyn = x` /
`d = x` / `return x` coercion sites are not yet wired — a primitive there
still flows in unboxed and mis-dispatches (pre-existing, no regression);
the `lower_dyn_arg` helper drops straight into those two sites once their
dyn-type detection is added. The self-host checker (`checker.fern`) still
does not enforce object-safety or the coercion rule; the Go checker is
the strict gate until it retires.

**A `dyn Trait[]` array literal in ARGUMENT position — wired (#6906).**
`render(["a", "b"])`, where `render`'s parameter is `dyn Show[]`, is the
third site: the destination is an ARRAY-typed parameter, which neither of
the two detectors above sees, so the elements were built into the buffer
raw. A struct/enum element carries its own shape and so survived that
(which is why the existing `dyn` tests passed); a primitive/string element
does not, and the callee's dispatch read a shape pointer out of a scalar
— SIGSEGV on the register backends, a validation failure on wasm.
`fn_param_sigs` now carries a `'8'` flag for a `dyn Trait[]` param, and an
array-LITERAL argument at such a position lowers through the shared
`lower_dyn_array_lit`, the same helper the `var xs: dyn Show[] = […]`
binding uses. Only the literal form needs it: every other argument is an
already-built array whose elements were coerced where it was built.

That gap was why `std/format`'s Display-accepting entry points are
`[T: cmp.Display]` bounds rather than `dyn cmp.Display[]` (#2684) —
`format(fmt, [40, 2, 42])` is exactly this shape, and the stdlib is
compiled by the self-host. Flipping the signature is now unblocked;
bounded generics monomorphise and work either way, so it is a choice
rather than a workaround.

Closing it needed a second fix, in the WAT→binary assembler rather than in
lowering. `emit_dyn_dispatch` writes a value-producing `if (result i32)`,
and in FLAT (unfolded) WAT that blocktype is a `(result …)` node sitting
in the instruction stream at the function's top level. `watbin.fern`'s
flat encoder hardcoded the empty blocktype `0x40` for every
`block`/`loop`/`if`, and its header readers filtered `(result …)` over the
whole func node — so the block's own type was both dropped from the body
and counted as a second FUNCTION result. A module the WAT path ran fine as
text failed to validate once assembled ("values remaining on stack at end
of block"). The declarations are now read as a PREFIX (`func_header_end`)
and a flat block consumes its own blocktype. This affected EVERY
`dyn Trait[]` program on the binary path, including the already-wired
local-binding site, so it is independent of the argument position — and
invisible to the WAT-based tests, which hand the text to wasmtime
directly. `TestSelfHostWasmBinary` is the leg that sees it.

The **wasm**
self-host backend (static-dispatch, no runtime shape-compare) handles
struct `dyn Trait` via `emit_dyn_dispatch`: it reads the receiver's struct id
(the offset-0 type tag) and branches to the matching `$Struct__method`
over every implementing struct — see TRAITS.md §7a slice 9.

**ENUM receivers on the IR path (#4785).** "A struct/enum value carries
its own shape" needs one qualification: an enum value's offset-0
identity is its **variant's** interned shape (the same identity
`op_variant_is` reads — `struct_make` with the variant name), never the
enum name itself. The AST backends handle this with a blanket
enum-receiver *fallback* arm (first enum method of that name wins —
`asm.fern`'s `is_enum_recv` chain), but the IR backends' `dyn_dispatch`
chains originally had **no enum arm at all**: an enum local coerced into
a `dyn` slot compared its variant shape against struct/prim arms, missed
everything, and fell through to the 0 fallback — wrong values from a
native-valid program. All three IR backends (`asm_ir.fern`,
`asm_arm64_ir.fern`, `wasm_ir.fern`) now emit a **per-variant arm** for
each `impl Trait for <Enum>` method: the receiver's shape is compared
against every variant of the enum (`enum_owner` walk; an OR-chain of
type-id compares on wasm), and a hit calls `<Enum>.<method>` with the
variant box as the receiver (its body's `match (self)` re-reads the
variant). Per-variant keying — unlike the AST fallback — means two enums
implementing the same trait dispatch to their own impls. Coverage:
`internal/e2eselfhost/self_host_dyn_enum_ir_test.go` (x86-64 + wasm +
arm64) and the native leg `internal/e2e/dyn_enum_dispatch_native_test.go`.
(The native x86-64 backend still segfaults on an enum LOCAL as a
`dyn Trait[]` array-literal element — #4787, separate coercion-side gap.)

### 4.4 RC of trait objects (Perceus follow-up — design)

Slices 2b–2d nailed dispatch but left every compiled `dyn Trait` value
**leaking**: the `data` word (the concrete heap object) is never dec'd,
and on the natives the box cell is never freed. The interpreter leaks
them too (it GC-frees nothing per-object), and a bounded leak in a
short-lived CLI / edge handler is survivable — but the language's whole
memory story is precise Perceus RC, so trait objects must join it. This
section is the design of record for that work (goal 2 in CLAUDE.md);
implement against it slice-by-slice and tick the phasing in §7.

**The hard part: the concrete type is erased.** A `dyn`'s drop has to
free a value whose static type is gone, so the drop must *dynamically
dispatch* — exactly what the vtable is for. The plan adds the
destructor to the vtable and routes `dyn` drops through it.

1. **Vtable gains a TRAILING drop slot.** Every `(trait, concrete)`
   vtable grows one slot **at the end, index `method_count`**, holding
   the concrete type's drop function (`dropFnNameFor(C)` →
   `__drop_struct_<C>` / `__drop_enum_<…>` / `__drop_tuple_<…>`), or a
   **null sentinel (0)** when `C` needs no drop (a flat struct of
   scalars). Trailing — *not* leading — is the load-bearing choice: it
   leaves the method slot indices (0..n-1) untouched, so `OpCallDyn`'s
   slot math is **unchanged on all three already-shipped backends**, and
   the drop slot can be added to one backend's emitter at a time without
   a coordinated slot shift. (A leading slot at index 0 would force
   `OpCallDyn` slot → `trait_index + 1` *everywhere at once*, or a
   backend-divergent slot offset — both worse for an incremental,
   one-backend-per-slice rollout.) The drop slot's index varies by trait
   (= the trait's method count) but is **statically known at every drop
   site**, because the value being dropped still has IR type
   `DynTraitType{Trait}` — the trait is not erased in the IR, only the
   concrete type is. `collectVtables` records the drop fn alongside the
   methods; each backend's vtable emitter appends it (wasm: a
   function-table index; natives: an absolute function pointer), matching
   the slot's existing word kind. Backends that haven't wired RC yet
   simply don't append the slot — harmless, since nothing reads past
   `method_count` until that backend's RC slice lands.

2. **`dropFnNameFor` learns `DynTraitType`.** It returns
   `("__drop_dyn_<Trait>", true)` — a per-trait helper (the trait fixes
   the drop-slot index `method_count`, which the helper bakes in). The
   helper takes the `dyn` value in its native shape and:
   - reads `vtable[method_count]`; if non-null, `call`s it with `data`
     (this runs the concrete destructor, which frees `data` and anything
     it transitively owns — e.g. a `String` field);
   - on the **natives** (boxed) then `__fern_free`s the 16-byte cell;
     on **wasm** (inline) there is no cell, so it stops after the
     concrete drop.
   The `vtable` word itself is static `.rodata`/data-segment — never
   inc'd or dec'd. (Per-trait rather than one universal helper precisely
   because the slot index is `method_count`; the alternative — a single
   `__drop_dyn` taking the slot index as an argument — also works and can
   be adopted if helper-count becomes a concern.)

3. **Perceus dec/drop insertion treats `dyn` as owning.** The
   inc/dec/borrow passes already classify pointer-shaped owned values;
   `DynTraitType` joins them (`ast.IsPointerType` is already true). At a
   `dyn` value's last use / scope exit the pass emits the drop:
   - *inline (wasm)*: the slot is two words `[data, vtable]` — feed both
     to `__drop_dyn`;
   - *boxed (natives)*: the slot is one word (cell ptr) — `__drop_dyn`
     reloads `data`/`vtable` from the cell, drops, frees the cell.
   **Borrow inference** keeps a `dyn` parameter that is only dispatched
   on (never stored or returned) **borrowed** — no drop, no leak, no
   double-free — identical to how a borrowed `struct` param is treated.
   `inc` of a `dyn` value (aliasing) inc's `data` only (the concrete
   object's own header); the static vtable is untouched. On the natives
   an `inc` that must keep the box alive across two owners either inc's
   the cell's own RC header **or** copies the cell — TBD at
   implementation; the first cut can forbid `dyn` aliasing (move-only)
   to dodge the question, matching how reuse analysis already prefers
   moves.

4. **Reuse / drop-specialisation** is out of scope for the first cut —
   trait objects are dropped, not reused — but the boxed cell is a
   fixed-size 16-byte allocation, an ideal future reuse token.

**The IMPL METHODS behind a vtable borrow every parameter, under every
ownership model.** `OpCallDyn` jumps through a function pointer read out of
the vtable, so the call site has no callee *name*:
`calleeParamOwnedByDefault` is never consulted and no caller-side retain is
emitted, for the receiver or for anything passed alongside it. A method a
vtable slot can reach therefore cannot own its params — under the owned model
its exit ran an `is_unique`-gated `__fern_box_free` on values nobody had
retained, freeing the caller's object out from under it. `paramVerdict`'s
`vtableDispatched` rung (fed by `vtableDispatchedMethods` over
`prog.Vtables`) is what states this; unlike the rungs below it, it does not
consult the escape facts, because an escaping param is just as unretained
here. #6465.

**Phasing.** Mirror 2b–2d: (a) wasm inline first (it has the simplest
drop — no cell), (b) x86-64 boxed (adds the cell free), (c) arm64 boxed
(structural mirror). Each slice ships e2e **leak/reclaim** tests in the
shape of `TestX86_64ArrayDropFree` / `DestructureHeapBumpBounded`: a
loop that creates and drops many `dyn` values and asserts the heap stays
bounded, plus a differential case where the concrete type transitively
owns an RC value (a `struct` with a `String` field behind `dyn`) to
prove the inner free runs through the vtable destructor.

**Slice (a) — wasm — SHIPPED.** `VtableDecl` gained a `Drop` field
(`collectVtables` records `dropFnNameFor(C)`; a primitive concrete
originally recorded "" — since #4351 it records `__drop_dynprim_<prim>`,
which frees the `boxPrimitiveDynValue` VALUE CELL behind `data` that the
null sentinel used to leak on every coercion. The string BUFFER behind a
string payload stays leak-mode: the coercion takes no retain, so an
aliased source must never be freed from the dyn drop); the wasm `internVtable`
appends it as the trailing function-table-index slot at index
`len(Methods)`. `dropFnNameFor` learns `DynTraitType` → `__drop_dyn_<set>`
(wasm only — declines on `ptrW==8` so the natives keep leaking, no
dangling call). `buildDynDropHelpers` synthesizes one `__drop_dyn_<set>`
per used trait set: it reads `vtable[methodCount]`, null-guards, and
reuses `OpCallDyn{slot: methodCount, sig: (i32)->i32}` to dispatch the
concrete destructor on `data` (the returned box ptr is dropped). The
Perceus passes treat a wasm `dyn` local as owning: `rcTracked`,
`computeFreeEligible`, the exit sweep (`emitDec`), and the loop-body
reinit drop (`emitOwnedSlotDrop`) all gained `DynTraitType` arms gated on
`ptrW==4`. A dispatched-only `dyn` param stays borrowed
(`paramOwnedByDefault` false → never dropped), so no double-free. Aliasing
is move-only (first cut — `needsRcIncOnAlias` declines `dyn`, so no inc).
The vtable-referenced drop fns are reached only by indirect call, so the
wasm build roots them (`ip.Vtables[*].Drop` + `__drop_dyn_*`) past IR
dead-function elimination, and the drop worklist is seeded from the
vtable drop slots. Tests: `internal/ir/vtable_test.go`
(`TestCollectVtablesDropSlot`) + `internal/e2e/rc_heap_bump_dyn_trait_test.go`
(bounded loop, no-underflow, multi-trait merged drop slot, borrowed-param
no-drop).

**Slices (b) x86-64 + (c) arm64 — boxed — SHIPPED.** Both natives now
reclaim a STANDALONE `dyn` local/param (pass `ir.DynRcSupported()`): the
boxed `__drop_dyn_<set>` helper reloads `data`/`vtable` from the cell,
dispatches the concrete dtor through the trailing vtable drop slot
(`vtable[methodCount*ptrW]`, an absolute fn pointer), and `__free`s the
16-byte cell. Tests: `rc_heap_bump_dyn_trait_x86_64_test.go` +
`rc_heap_bump_dyn_trait_aarch64_test.go`.

**§7.8 follow-up — `dyn` NESTED INSIDE A CONTAINER — array element
SHIPPED on all backends.** Standalone `dyn` reclaim left a `dyn` value
held *inside a container* leaking on the natives: the container's
recursive-drop path called `dropFnNameFor(elemType, …,
dynRcSupported=false)`, declining a `DynTraitType` element. The headline
container is **`dyn Shape[]`** (the §2 motivating example — a
heterogeneous array). It now reclaims on x86-64 + arm64 + wasm via a
dedicated `__drop_arr_dyn_<__drop_dyn_<set>>` array drop:
`arrElemStructDropName` gained a `DynTraitType` arm (gated on the
backend's dyn-RC capability — `ptrW==4 || dynRcSupported`) that names it,
and `genArrDynDropFn` builds a per-element loop that runs the per-set
`__drop_dyn_<set>` destructor on each element, then frees the outer
buffer. The loop is **representation-aware** in two ways the generic
`genArrOfArrDropFn` cannot express, hence a dedicated generator:
(1) a `dyn` element is ONE word (the boxed cell ptr, stride `ptrW`,
`WidthPtr` load, 1-arg call) on the natives but TWO words (inline
`[data, vtable]`, stride `2*ptrW`, `WidthString` load, 2-arg call) on
wasm; (2) `__drop_dyn_<set>` returns VOID, so there is NO trailing
`OpDrop` after the per-element call (unlike `genArrOfArrDropFn`, whose
perElem returns the i32 box ptr). The `dyn-RC` flag is threaded to
`arrElemStructDropName` at the array LOCAL drop sites (the `b.`
exit-sweep / reinit / precise-drop callers carry `b.dynRcSupported`);
the worklist regenerates `__drop_arr_dyn_*` from the name and seeds
`__drop_dyn_*` (always emitted by `buildDynDropHelpers`). Tests:
`internal/e2e/rc_heap_bump_dyn_trait_container_test.go` (bounded loop +
no-underflow, heterogeneous `Circle`+`Rect` each owning a String, on
x86-64 + arm64 + wasm).

**§7.8 follow-up — `dyn` as an ENUM PAYLOAD — SHIPPED on the natives.**
An `enum` variant carrying a `dyn` payload (`enum Box { Wrap(dyn Shape),
Empty }`) now reclaims its boxed `dyn` (cell + concrete + any String the
concrete transitively owns) on **x86-64 + arm64**. The dyn-RC capability
is threaded through the enum drop sites: `enumVariantDropPlan`'s
`dropKind` admits a `DynTraitType` payload, and the shared child-drop
helpers — the generated `appendChildDrop` (used by `genEnumDropFn` /
`genTupleDropFn` / `genStructDropFn`) and the inline `dropStructField`
(used by `emitEnumSlotDrop`'s variant-plan tier at the exit sweep) — gained
a `DynTraitType` arm that calls the per-set `__drop_dyn_<set>` destructor
on the boxed one-word cell ptr (argc 1, VOID return, so NO trailing
`OpDrop`, mirroring `genArrDynDropFn`). The payload is loaded with
`payloadLoadOpFor`, which is `WidthPtr` (one word) on the natives —
matching the helper's argc. Both the per-iteration loop-var reinit drop
(generated `__drop_enum_<N>` route, via `emitEnumDropViaGenFn` whose gate
now passes `b.dynRcSupported`) and the once-per-call exit-sweep
(`emitEnumSlotDrop`) reclaim the payload. Tests:
`internal/e2e/rc_heap_bump_dyn_trait_nested_test.go` (bounded loop +
no-over-release, the matched-and-bound `match (b) { Wrap(s) => … }` shape
— the double-free-sensitive case — on x86-64 + arm64).

**wasm is deliberately EXCLUDED for the enum-payload kind.** wasm's INLINE
two-word `dyn` representation double-drops when the payload is
matched-and-bound: the bound `s` and the container's payload drop both
reclaim the same `data` (the inline value is copied, not shared via a
cell), tripping a "pointer not aligned" freelist corruption. So the
enum/struct/tuple child-drop dyn arms gate on **`dynRcSupported` ONLY**
(natives, `ptrW==8`), not `ptrW==4 || dynRcSupported`. wasm keeps its
prior correct-but-leaking behaviour for the enum-payload kind; the
ARRAY-element kind is unaffected (it reclaims on wasm via the separate
`genArrDynDropFn` path, whose dedicated per-element generator has no
matched-and-bound aliasing).

**§7.8 — already-reclaiming and still-FLAGGED kinds (follow-up).**

- **Closure capture of a plain `dyn`** — SHIPPED on the natives
  (x86-64 + arm64). A captured `dyn` is moved into the env *without an inc*
  (`needsRcIncOnAlias` declines `dyn`, move-only). The first cut *assumed*
  the source local's exit-sweep drop (`emitDec`'s `DynTraitType` arm)
  reclaimed it once and the thunk must NOT — true ONLY for a NON-escaping
  closure. For an **escaping** closure (`return function () { … d.m() … }`)
  the source local is NOT swept (it escaped into the returned env), so the
  capture leaked — worse, the standalone-`dyn` reclaim's sweep predicate did
  not exclude an escaped capture, so the source-local drop **freed the cell
  the returned closure still dereferenced** — a use-after-free that
  SEGFAULTED. Now both halves are wired on the natives:
  `markConstructionMoves`' MakeClosure arm marks a `dyn` capture MOVED
  (suppressing the source-local exit-sweep drop on EVERY path, escaping or
  not), and `genClosureDropThunk` reclaims the captured `dyn` via the per-set
  `__drop_dyn_<set>` destructor (boxed one-word cell ptr → vtable's trailing
  drop slot → concrete dtor → `__free` the cell; argc 1, VOID return, so NO
  trailing `OpDrop`). Net: no MakeEnv inc + one suppressed source drop + one
  thunk reclaim = exactly one reclaim. `hasRcCapture` counts a `dyn` capture
  (so the named thunk is generated + selected by `emitDec`). NATIVES ONLY:
  all three sites gate on `dynRcSupported` (NOT `dynReclaim`, which includes
  wasm) so the suppress/reclaim pair stays consistent. **wasm's**
  closure-captured `dyn` keeps its prior correct-but-leaking behaviour (its
  inline two-word env copy isn't reclaimed, the thunk declines it, and the
  source local stays swept) — and an escaping `dyn`-capturing closure on
  wasm is a PRE-EXISTING dispatch bug (returns the wrong value), independent
  of RC. Tests: `rc_heap_bump_dyn_trait_closure_test.go` (bounded loop +
  no-underflow, an ESCAPING closure capturing a String-owning Circle behind
  `dyn Shape` — the use-after-free-sensitive shape — on x86-64 + arm64).
- **Tuple element** holding a `dyn` (`(dyn Shape, i32)`) — FLAGGED-LEAKING.
  Pre-existing DISPATCH bug, not an RC gap: `t.0.area()` traps on wasm
  ("indirect call type mismatch") and segfaults on the natives, so the
  tuple-of-`dyn` cannot be exercised end-to-end. RC of a kind that doesn't
  dispatch is moot/unsafe; deferred behind a tuple-`dyn` dispatch fix.
- **Map value** of a `dyn` (`Map[K, dyn Shape]`) — FLAGGED-LEAKING
  (DELIBERATE; a documented leak beats a double-free). Map-of-`dyn`
  *dispatches* fine on the natives (`m.get(k).area()` works — verified), but
  its boxed `dyn` value cannot safely join the map's value-reclamation
  machinery. The blocker is the **headerless boxed `dyn` cell**: `OpBoxDyn`
  allocates the `{data, vtable}` cell with plain `__fern_alloc` (NO rc
  header — that is why standalone `__drop_dyn` uses `__free`, not
  `__fern_rc_dec`). The map's value reclamation (`mapValKindTag` kind 4 →
  `mapValHasDrop` → the `__drop_map_via_<perVal>` column walk) is built
  around an **rc-headered** value: kind ≥ 2 makes the runtime
  `__map_retain_val` call `__fern_rc_inc(v)` on every `get`/`get_or`/
  `values`/`iter`, so bumping `dyn` to kind 4 would `__fern_rc_inc` a
  headerless cell — heap corruption. Routing `dyn` as kind 1 with a
  *drop-only* (no-retain) column walk avoids the corruption but reintroduces
  a hazard: a `dyn` read out via `get` and kept past the map's life would be
  freed by the map drop (no retain co-owns it) → use-after-free — and `dyn`
  is move-only (`needsRcIncOnAlias` declines it), so it can't be retained out
  of the map under the current model anyway. So `mapValHasDrop` keeps
  `dynRcSupported=false` for a `dyn` value: it reads kind 1, the value
  leaks, and NOTHING double-frees. Cleanly wiring it needs either an
  rc-headered boxed `dyn` cell (a `__fern_alloc_rc1`-based representation —
  a large change touching every standalone/array/enum `dyn` reclaim path) or
  a dyn-specific drop-only map path with proven no get-escape; both are out
  of scope for this slice. wasm map-of-`dyn` additionally fails core-module
  validation (pre-existing dispatch gap), so it is doubly out of scope.
- **`dyn Trait[]` array CAPTURED by a closure** (`genClosureDropThunk`'s
  `arrElemStructDropName(…, false)` site) — still FLAGGED-LEAKING; the
  capture inc/borrow accounting for a nested-array `dyn` isn't established.

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
3. **Slice 3: self-host parity (x86-64 + arm64 + wasm — shipped).**
   `dyn Trait` parses in the self-host and dispatches over its existing
   shape-pointer path on x86-64 + arm64; the wasm self-host backend
   dispatches via `emit_dyn_dispatch` (struct-id compare-branch). All
   self-host backends handle struct/enum concrete types end-to-end (see
   §4.3). Remaining: the strict object-safety / coercion checks in
   `checker.fern` (only needed once the Go checker retires).
4. **Slice 4: RC of trait objects (Perceus — designed in §4.4).** Stop
   leaking the boxed object: a TRAILING vtable drop slot + a per-trait-set
   `__drop_dyn_<set>` that runs the concrete destructor (and, on natives,
   frees the box cell), wired into the inc/dec/borrow passes. Phased
   wasm-inline → x86-64-boxed → arm64-boxed, each with leak/reclaim
   tests. **Slice 4a (wasm inline) SHIPPED** — `dyn` values are reclaimed
   on wasm (the transitively-owned String behind `dyn` frees through the
   vtable destructor; multi-trait merged-slot drop works; a borrowed
   dispatched-only `dyn` param is not dropped). x86-64 + arm64 still leak
   (slices 4b/4c next), their vtable emitters simply not appending the
   drop slot yet (harmless). See §4.4 for the shipped wasm details.
5. **Downcast slice 1 (shipped): `e as? T` on the interpreter.** Surface
   syntax + checker + interp for the fallible downcast (§9). Struct/enum
   targets.
6. **Downcast slice 2 (shipped): `e as? T` codegen on all three
   backends.** The vtable-pointer compare (§9), lowered target-agnostically
   in `(*builder).emitDowncast` so wasm / x86-64 / arm64 inherit it with no
   new IR op; the `data`/`vtable` extraction reuses the dispatch lowering's
   `b.dynBoxed()` branch and the `Some`/`None` construction reuses the
   ordinary heap-box `Option` representation. Downcast-only targets are
   rooted via `treeshake.DowncastImplMethods`. Struct/enum targets;
   primitive targets remain a follow-up.
7. **Multi-trait objects (`dyn A + B`) — slices 1 + 2 + downcast shipped.**
   Slice 1: surface + checker + interpreter. Slice 2: merged-vtable
   **dispatch** codegen on all three backends (wasm + x86-64 + arm64) —
   single-trait `dyn A` lowers byte-for-byte as before. The multi-trait
   `as?` **downcast** now also lowers on all three native backends (and the
   self-host, which is shape-based) via the MERGED-vtable pointer compare
   (`emitDowncast` keys `OpConstVtable` by `dynVtableSetKey(dc.Traits)`).
   Spec + status in **§10**. Remaining: disambiguation syntax for a method
   declared by two traits.
8. **Follow-ups.** Explicit upcast, primitive downcast targets, `dyn` in
   struct fields with the fat-pointer layout, and RC of the produced
   downcast `Option`/`data` (§4.4).

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

## 9. Downcast — `e as? T` (fallible recovery of the concrete type)

A **downcast** recovers a concrete type from a trait object. It is the
runtime-checked counterpart to coercion (§5): coercion erases a concrete
type into a `dyn Trait`; the downcast asks, at run time, "is this
`dyn Trait` *actually* a `T`?" and answers with an `Option`.

```fern
function describe(s: dyn Shape): string {
    var c: Option[Circle] = s as? Circle;       // Some(circle) | None
    match (c) {
        Some(x) => "circle r=" + x.r.to_string(),  // x: Circle — usable concretely
        None    => "other",
    }
}
```

**Syntax.** `e as? T`. The `?` distinguishes it from the numeric/ascription
cast `e as T` (§ — `as` and `as?` are different operators producing
different AST nodes: `CastExpr` vs `DowncastExpr`). `as?` binds at the same
precedence tier as `as`.

**Semantics.** For `e: dyn Trait` and a concrete `T` that implements
`Trait`, `e as? T` evaluates to **`Option[T]`**:
- `Some(v)` when `e`'s runtime concrete type is **exactly** `T` — and the
  bound `v` is usable as a `T` (field access, methods, …);
- `None` otherwise.

The match is on the *exact* concrete type, not on "also implements `Trait`"
or any subtyping relation (there is no trait inheritance in v1). `as?`
always returns an `Option`, distinct from `as`, which never does.

**Type rules (checker).** `e` must have type `dyn Trait` — a non-`dyn`
left operand is `error[E059]` (`'as?' downcast requires a 'dyn Trait'
value on the left`). `T` must implement `Trait` (the same
`Info.Impls[Trait][T]` gate coercion uses) — otherwise `error[E060]`
(`T does not implement Trait, so a 'dyn Trait' cannot downcast to it`).
The checker records the inner's trait on the node (`DowncastExpr.Trait`)
for later codegen.

**Slice-1 scope: struct/enum targets only.** `T` must be a concrete
struct or enum (`error[E060]` otherwise). Primitive / `string` downcast
targets (`dyn Display as? i32`) are a follow-up — they share the
primitive-boxing boundary the dispatch slices hit (§4.2.3), since a
primitive carries no runtime type tag in the compiled representations.

**Implementation — interpreter (slice 1).** The interpreter's boxed `dyn`
value *is* the concrete `Struct`/`Enum`, which already carries its
`TypeName`/`EnumName`; the downcast recovers that name (`valueTypeName`)
and compares it to the target's name, building `Some(value)` / `None`
through the existing `optionSome` / `optionNone` helpers.

**Codegen (slice 2 — shipped on all three backends).** The compiled path
is a single **vtable-pointer compare**: the `(trait, concrete)` vtable
already uniquely tags the concrete type, so a downcast to `T` is "does
this `dyn`'s vtable word equal `__vtable_<Trait>_<T>`? then `Some(data)`
else `None`" — no new runtime metadata, reusing the slice-2 dispatch
vtable infrastructure (§4.2), and **no new IR op**. `(*builder).emitDowncast`:

1. lowers the receiver to its `dyn` value and extracts the `data` and
   `vtable` words into i32 scratch locals using the **exact** extraction
   the dispatch lowering uses (`b.dynBoxed()`: natives deref the
   `{data@0, vtable@ptrW}` cell, wasm pops the two-word inline value's
   high word);
2. emits `OpConstVtable{Trait, T}` (the same static address a coercion of
   `T` produces — pointer-identity holds) and `OpEq` against the
   extracted vtable;
3. branches with a `BlockTypeVoid` `OpIf`: hit → builds an ordinary
   heap-box `Option[T].Some(data)` (`data` is the concrete heap pointer,
   which IS the `T` value — no unbox for a struct/enum target) into a
   scratch slot via `emitOptionSomeFromSlot`; miss → the shared
   payloadless `OpEnumSentinel{1}` (`None`); then loads the slot.

The result is an ordinary `Option` heap-box a `match` reads with the same
`[ptr+0]` tag load as any other, so nothing downstream is downcast-aware.

The `rejectDowncast` gate in `LowerWith` now only fires for a backend that
has **not** lifted its `dyn` gate (`!dynSupported`) or — defensively — a
non-struct/enum target (the checker blocks primitives with E060 first).
**Multi-trait `dyn A + B` downcast is supported** (§10): `emitDowncast`
keys `OpConstVtable` by the whole trait set (`dynVtableSetKey(dc.Traits)`),
so the compare references the MERGED `__vtable_<A+B>_<T>` cell a multi-trait
coercion of `T` stores — exact for any set, and byte-identical to the
single-trait path for a 1-element set (the set key collapses to the bare
trait name). A
**downcast-only target** `T` (never coerced to `dyn Trait`, only downcast
to) is absent from `Info.DynCoercions`, so its `__method_*` would be
tree-shaken / IR-dead-function-eliminated and the vtable cell would
reference a missing symbol; `treeshake.DowncastImplMethods(prog, info)`
roots them on every backend (rooting **every** trait in the set, not just
the primary, so a multi-trait downcast-only target's full merged vtable
links — and the wasm path also feeds them to its IR-level `LiveFunctions`
cull). RC of the produced `Option`/`data` is out of scope (leak-mode, like
the rest of `dyn` — §4.4).

**Tests.** Parser (`DowncastExpr` vs `CastExpr` disambiguation); checker
(valid → `Option[T]`, `DowncastExpr.Trait` stamped, non-`dyn` LHS →
E059, non-impl target → E060); e2e interp + compiled — `TestInterpDowncast`
(interp behaviour + all three targets now compile) and the differential
`TestDowncast*` (`dyn_trait_compiled_test.go`, via `dynAllBackends`):
struct hit/miss, heterogeneous `dyn Shape[]` per-element count, an
enum-with-payload target, a downcast-only-target tree-shake guard, plus the
multi-trait cases — `TestDowncastMultiTraitHitMiss` (`dyn A + B` hit on the
matching concrete / miss on a different one that also impls both),
`TestDowncastMultiTraitOnlyTargetRooted` (multi-trait downcast-only target),
and `TestDowncastThreeTrait` (`dyn A + B + C`). The self-host's shape-based
downcast handles any trait set for free (`downcast-multi-*` cases in
`self_host_dyn_trait_ir_test.go`).

## 10. Multi-trait trait objects — `dyn A + B`

A trait object can span **multiple** traits: `dyn A + B` is usable as
both `A` and `B`. This generalises the single-trait `dyn A` (the
1-element case, which stays 100% behaviour-identical).

```fern
trait Show  { function show(self: Self): string; }
trait Weigh { function weight(self: Self): i32; }
struct Apple { g: i32 }
impl Show  for Apple { function show(self: Self): string { return "apple"; } }
impl Weigh for Apple { function weight(self: Self): i32 { return self.g; } }

function describe(d: dyn Show + Weigh): string {
    return d.show() + "=" + d.weight().to_string();   // a method from EACH trait
}
```

**Syntax.** `dyn A + B + C` — the first trait name followed by zero or
more `+ Trait` (mirroring trait-bound syntax). Each name may be
module-qualified (`dyn mod.A + B`) and the whole thing takes the usual
postfix `[]` (`dyn A + B[]` is an array of multi-trait objects). A
trailing or empty `+` is a parse error.

**Set semantics (sorted + deduped, order-insensitive).** The trait set
is normalised to a sorted, deduplicated list at construction
(`ast.NewDynTraitType`), so `dyn A + B` ≡ `dyn B + A` ≡ `dyn A + B + A`.
`ast.Equal` is therefore a plain element-wise compare and `String()` is
deterministic (`"dyn A + B"`). The representation is
`DynTraitType{Traits []string}`; `Trait0()` reads the first trait for
genuinely single-trait-only contexts (the compiled codegen path, which
only lowers single-trait `dyn` today), while multi-trait-aware code
iterates `Traits`.

**Object-safety — ALL.** Every trait in the set must exist, be a trait,
and be object-safe (§3). `validateDynTraitTypes` reports per offending
trait, so `dyn Bogus + Eq` surfaces both the unknown trait and the
non-object-safe one.

**Coercion — impl-ALL.** A concrete `C` coerces to `dyn A + B` iff `C`
implements **every** trait in the set (`Info.Impls[t][methodTypeName(C)]`
for all `t`). The checker's `assignable` gate runs `implementsAllDynTraits`
so every boxing site (var init, assignment, argument, array element,
return) is covered uniformly; a failure names the missing trait(s).

**Method resolution — UNION, collision = error.** A call `d.m()` resolves
`m` across the union of the traits' method sets:
- exactly one trait declares `m` → use it (the owning trait is recorded
  on `Call.DynTrait` for codegen / dispatch);
- two or more traits declare `m` → an **ambiguity error** (`E062`);
  disambiguation syntax (e.g. `d.(A::m)()`) is a follow-up;
- none → `no method "m" on dyn A + B` (`E021`).

Dispatch is still by the receiver's **runtime concrete type** (the
interpreter's `valueTypeName` tag), so once `m` is resolved the call
lands on `C`'s impl exactly as for single-trait `dyn`.

**Status — slices 1 + 2 + downcast shipped (interp + merged-vtable
codegen + multi-trait downcast).**
Slice 1 shipped the surface (parser), the checker (validity / coercion /
resolution / ambiguity), and the interpreter (dispatch from any trait in
the set, heterogeneous `dyn A + B[]`, passing to a `dyn A + B` param) —
mirroring how single-trait `dyn`, `as?` downcast, and block-expressions
shipped interp-first. Slice 2 lifts the compiled gate: **merged-vtable
dispatch now lowers on all three backends** (wasm + x86-64 + arm64),
matching the interpreter. Single-trait `dyn A` lowers exactly as before
(byte-for-byte — the merged key collapses to the bare trait name for a
1-element set). The `e as? T` **downcast** of a multi-trait value now
also lowers on all three native backends (and the self-host): `emitDowncast`
keys the vtable-pointer compare by the whole set (`dynVtableSetKey(dc.Traits)`),
so it references the same MERGED `__vtable_<A+B>_<T>` cell a multi-trait
coercion of `T` stores — the compare matches exactly when the runtime
concrete is `T` (§9). `treeshake.DowncastImplMethods` roots **every** trait
in the set's impl methods, so a multi-trait downcast-only target's full
merged vtable links. Single-trait downcast is unchanged. The only
multi-trait sub-case still missing is disambiguation syntax for a method
declared by two traits (a parser follow-up).

**Merged-vtable codegen (shipped).** For `dyn {A,B}` (sorted set) over
concrete `C`, the vtable is the **concatenation** of the per-trait tables
in the set's sorted order: `[ C's A-methods (A decl order)…, C's B-methods
(B decl order)… ]`. A method `m` owned by trait `T` dispatches at **global
slot = (sum of method counts of traits ordered before T in the set) +
m's index within T** (`ir.dynTraitMethodPrefix` + the local slot). The
single-(trait,concrete) `collectVtables` infrastructure (§4.2) generalises
to a (trait-set,concrete) key:

- `collectVtables` keys each merged `VtableDecl.Trait` by
  `ir.dynVtableSetKey(traits)` — the sorted traits joined with `+`
  (`"A+B"`); a 1-element set is the bare trait name, so single-trait
  tables are unchanged. Its methods concatenate the per-trait slot lists
  (`ir.traitVtableSlots`), and its concrete set is the **intersection** of
  the traits' implementors (a `C` coerces only if it impls every trait).
- `OpConstVtable.Str` carries the same set key at both the coercion site
  (which stores the merged vtable address) and the dispatch site.
- `OpCallDyn.I32` is the global slot, computed at the `*ast.Call` lowering
  from the receiver's static set (`Call.Method.Receiver`, a
  `DynTraitType`) + the owning trait (`Call.DynTrait`).
- The natives sanitize the `+` in the set key to `_x_` when building the
  GAS label (`dynVtableLabel`), since `+` isn't a valid assembler-label
  char; wasm keys by data-segment offset and needs no sanitisation.
- `DynCoercion.Traits` records the whole set so tree-shaking
  (`treeshake.DynCoercionImplMethods`) already roots the impl methods of
  every trait, and `ir.dynTraitSetsUsed` collects each whole set used.
