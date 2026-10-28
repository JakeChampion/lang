# Const generics

Status: [plan] CG1 and CG2 ship. A function, a struct, an enum, a type
alias, an impl or a trait takes a const parameter. `Vec[N, T]` is the second
spelling of the lane types (§4). CG3a and CG3b ship, and CG3c is a plan.

A const parameter is a generic parameter whose argument is an integer, not a
type. `Vec[N, T]` is the first consumer: it is a lane type of `N` lanes of
`T`, and `f64x2` and `f32x4` become its two spellings on the portable tier
(`docs/SIMD-GENERIC-KERNELS.md` §3.2). The decision to build const generics
after the lane types was taken on 2026-10-21.

## 1. The language

### 1.1 Declaring a const parameter

A const parameter is `const NAME: i32` in a generic parameter list. It can
go in the list of a function, a struct, an enum, a type alias, an impl or a
trait, in any position:

```fern
struct Ring[const N: i32, T] { items: T[], head: i32 }

function filled[const N: i32](x: f64): f64[] {
  let out: f64[] = [];
  while (out.len() < N) {
    out = out.append(x);
  }
  return out;
}
```

The `const` keyword marks the parameter. A `:` after a type parameter already
opens a trait bound (`[T: Add]`), so the keyword keeps the two kinds apart
without a lookahead. A const parameter has no trait bound. Its type is `i32`
only, the type of `len()` and of an index. A later slice can admit `usize`,
`u8` and `boolean` without a change to the syntax.

### 1.2 Applying a const argument

A const argument is an integer literal that fits in `i32`, in any base, or a
const parameter in scope:
`Ring[8, string]`, `Vec[2, f64]`, `Vec[N, T]`. An expression such as
`N + 1` or `2 * N` is refused (§1.6). Arithmetic on const arguments needs the
checker to prove two expressions equal. Rust's first release of const
generics made the same cut for that reason.

An argument must match its parameter's kind. A type in a const position, or
an integer in a type position, is refused.

### 1.3 A const parameter in a body

In a body, `N` is a value of type `i32` and a constant. After
monomorphisation it is the literal of its instance, so the existing constant
folding sees it. `N` is not a place: an assignment to it is refused (E091).
A binder of the same name in the body shadows `N`, as it shadows a parameter.
A value parameter of the same name is a duplicate parameter (E018).

### 1.4 Inference and explicit arguments

A call infers a const argument the way it infers a type argument, from the
types of its arguments. `Vec[N, T]` unified with `Vec[2, f64]` binds
`N = 2` and `T = f64`. A lane type unifies as its `Vec` spelling, so
`f64x2` binds `N = 2` and `T = f64` too (§2).

The program writes a const argument that no argument type carries:
`filled[4](1.0)`. When one argument is written, all are written, in the order
of the parameter list, as for type arguments today.

### 1.5 Type identity

`Ring[8, i32]` and `Ring[16, i32]` are different types. Two applications of
one declaration are the same type when every type argument is the same type
and every const argument is the same integer.

### 1.6 Diagnostics

The design adds these codes:

- E089: a const argument that is not an integer literal or a const parameter.
- E090: an argument of the wrong kind. That is a type for a const parameter,
  or an integer for a type parameter.
- E091: an assignment to a const parameter.
- E096: a `Vec[N, T]` with no lane type on the portable tier (§2.2).

A const parameter in the list of a method is a P001. A method gets the const
parameters of its receiver from the receiver spelling:
`function (r: Ring[K, T]) cap()` gives the method K. An impl method gets the
const parameters of its impl. A default method of a trait reads the impl's
argument for the trait's const parameter.

A const argument that cannot be inferred reports E040, as a type argument
does.

## 2. `Vec[N, T]`

### 2.1 The spellings

`Vec` is a builtin type constructor with one const parameter and one type
parameter. `Vec[2, f64]` is `f64x2` and `Vec[4, f32]` is `f32x4`. Each pair
is one type with two spellings, and the formatter keeps the spelling that
the program wrote. A diagnostic names the lane type: `f64x2`, not
`Vec[2, f64]`. The lane builtins and the `Lanes` surface do not change.

The two pairs always name the lane types. A declared type named `Vec` takes
every other application of the name.

### 2.2 Which pairs exist

The portable tier has a lane type only where `N` lanes of `T` fill 128 bits
and every target has the instructions: `Vec[2, f64]` and `Vec[4, f32]`.
Any other pair is refused (E096) where it is written with concrete
arguments. A generic `Vec[N, T]` that no pair can fit, such as `Vec[3, T]`,
is refused too. `i32x4`, `i64x2` and `u8x16` join the table when their lane
types land, through the same rule.

In a generic body, a `Vec[N, T]` that names a type parameter is a type
parameter of its own. Its bounds are the traits that every lane type it can
stand for implements. `Vec[N, T]` takes the traits that both `f64x2` and
`f32x4` implement, and `Vec[N, f64]` takes the traits of `f64x2`.

A method or an operator on a generic `Vec` resolves as it does for
`T: Lanes`. A method that one of the lane types does not provide is E021. A
call infers `N` and `T` from a lane type argument. An argument that is none
of the lane types that the parameter can stand for is E038.

### 2.3 An impl for a generic `Vec`

An impl whose type is a generic `Vec` is one impl for each lane type that the
spelling can stand for:

```fern
impl[const N: i32, T] Total for Vec[N, T] {
  function total(self: Vec[N, T]): f64 { ... }
}
```

This is `impl Total for f64x2` with `N = 2` and `T = f64`, and
`impl Total for f32x4` with `N = 4` and `T = f32`. Each copy reads `N` as its
integer. `impl[const N: i32] Count for Vec[N, f64]` is an impl for `f64x2`
only. Each copy is checked as a concrete impl, so a body that one lane type
cannot run is E043 for that lane type. An impl for a `Vec` that no pair can
fit is E096. A module that declares its own `Vec` keeps the impl as written.

### 2.4 What it gives a kernel

A kernel can name its lane count in its signature, and step through an array
by `N` in its body:

```fern
function scaled[const N: i32](own xs: f64[], k: Vec[N, f64]): f64[]
```

The same kernel can also take a type parameter `T: Lanes` and read
`T.LANES`. A body over `Vec[N, T]` reads `N`. The index of a lane builtin
stays an integer literal (E038). The `lane` method of `Lanes` takes a
variable index.

## 3. Where it goes

The paths below are from a survey of the tree on 2026-10-22. They name the
places each compiler has to change.

### 3.1 The self-host compiler

- **Syntax.** `parse_type_param_list` (`compiler/parser.fern`) reads a `:`
  as a trait bound, and `parse_decl_tparams` refuses a `:` on a struct or an
  enum. Both learn `const NAME: i32`. The type-application collector in
  `parse_type_ident_led` stops at a number token, and `type_shaped_bracket`
  needs an identifier first, so both learn a number. `parse_func_decl` must
  always keep a const parameter in `type_params`. Today it erases an
  unbounded parameter.

- **Spelling to type.** A type spelling is a string, and `"2"` already passes
  through `bind_unify`, `subst_ty`, `sanitize_key` and `parse_type_ref`
  unchanged. `typeinfo.Type` gets a variant for a const argument, and
  `type_from_ref_names` makes it.

- **Identity.** `type_eq` compares structs by name. `nominal_args_clash`
  compares arguments, but it skips an argument that is unknown on either
  side. So the syntax alone makes `Ring[8, i32]` and `Ring[16, i32]` one
  type. Both learn the const variant.

- **Instances.** `ssasem.instance_name` keys an instance by `type_key`, and
  `leaf_key` learns the const variant. The parser-level monomorphiser names
  an instance `f__2__f64` already.

- **A const parameter in a body.** `subst_expr` replaces a type parameter
  with an identifier that names the type. For a const parameter it must give
  an integer literal. The self-host interpreter binds a type parameter as a
  type value (`bind_tyargs`), and binds a const parameter as an integer.

### 3.2 The Go front end

- **Syntax.** `parseTypeParamListWithArgs` and `parseDeclTypeParams` learn
  `const NAME: i32`. A type application parses each argument with
  `parseType`, and `peekTypeArgs` refuses a number. Both learn one.

- **Types.** `ast.Type` gets a node for a const argument. `ast.Equal`,
  `unifyType`, `resolveType`, both `substituteType` (the checker's and the
  monomorphiser's) and `ast.MangleArg` learn it.

- **A const parameter in a body.** The monomorphiser replaces the
  identifier with an integer literal. The interpreter sees only
  monomorphised code and needs no change.

## 4. Slices

- **CG1a (done).** `const N: i32` in the list of a function, and a const
  argument at a call site: `filled[4](x)`, `count[3, string](x)`,
  `filled[M](x)`. This is in both front ends, both printers and the grammar.
  `N` is an `i32` in the body, and each instance holds its literal. A call
  writes its arguments, because no argument type carries a const argument
  yet. The gate is the conformance case `const_param_functions` on every
  engine and E089, E090 and E091.

- **CG1b (done).** A const parameter on a struct, and a const argument in a
  type application: `Ring[4, string]`, `Ring[N, T]`. `Ring[4, i32]` and
  `Ring[8, i32]` are different types in both checkers.

  A call infers a const argument from an argument type. A method reads the
  const parameters that its receiver names. A struct literal takes them from
  its expected type, or it writes them (`Ring[4, i32] { ... }`). The gate is
  the conformance case `const_param_structs` on every engine.

- **CG1c (done).** A const parameter on an enum and an impl:
  `enum Slot[const N: i32, T]`, `impl[const N: i32, T] Cap for Ring[N, T]`.
  A method on `Slot[N, T]` reads N. Each instance of an impl gets its own N,
  also through a trait bound. A variant payload takes its type arguments
  from the expected enum instance (#13767), so `Full(Ring { ... })` gets N
  from `Slot[4, string]`. The gate is the conformance case
  `const_param_enums_impls` on every engine.

- **CG1d (done).** A const parameter on a type alias and a trait:
  `type Slot[const N: i32] = Ring[N, i32] | Gone`, `trait Fixed[const N: i32]`.
  A bound and an impl write an argument for it: `F: Fixed[4]`,
  `impl Fixed[8] for Box`. A default method of the trait reads N as the
  impl's argument. A member literal of a union alias takes its type
  arguments from the expected alias instance (#13784). The gate is the
  conformance case `const_param_aliases_traits` on every engine.

- **CG2 (done).** `Vec[N, T]` as the second spelling of the lane types, and
  inference of `N` and `T` from a lane type. A generic `Vec[N, T]` takes the
  traits that every lane type it can stand for implements, and a pair with no
  lane type is E096. The gate is the conformance case `vec_lane_kernels` on
  every engine.

- **CG3.** `std/simd` writes its kernels over `Vec[N, T]` where a lane count
  is named. It ships in three parts.

  - **CG3a (done).** The semantic graph unrolls a small loop that runs a
    constant number of times and whose body reads its counter
    (`compiler/semunroll.fern`). Each copy of the body reads the induction
    variable as a constant. A constant fold then
    lets `semthread` remove the branches of a lane index that an `@inline`
    method tests. Thus a lane loop `while (i < N)` costs the same as the
    lanes written out one by one.

    The gate is the conformance case `loop_unroll_counted` on every engine,
    and `TestSelfHostSemanticUnrollCountedLoops`.
  - **CG3b (done).** `@inline` and `@noinline` on an impl method, and an
    impl for a generic `Vec` (§2.3) in both front ends. The gate is the
    conformance case `vec_generic_impls` on every engine.
  - **CG3c.** `Lanes.with_lane` with a variable index, and one
    `impl Math for Vec[N, T]` in place of the two per-lane impls of
    `std/simd`.

## 5. Not in this design

- Arithmetic on const arguments (`N + 1`).
- A const parameter of a type other than `i32`.
- A default for a const parameter.
- Fixed-size arrays (`[N]T`). An array keeps its length at run time.

## 6. Comptime

`docs/COMPTIME-BRIEF.md` keeps comptime deferred, with monomorphisation for
genericity over types and constant folding for values. This design stays
inside that posture. A const argument is a literal that monomorphisation puts
into each instance and that constant folding then sees, and no expression
runs at compile time. Arithmetic on const arguments (§5) is where the two
systems start to need each other. That is a trigger in the comptime brief,
so it is not part of this design.
