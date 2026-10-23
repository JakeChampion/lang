# Const generics

Status: [plan] the design and the staging. Nothing here ships yet.

A const parameter is a generic parameter whose argument is an integer, not a
type. `Vec[N, T]` is the first consumer: it is a lane type of `N` lanes of
`T`, and `f64x2` and `f32x4` become its two spellings on the portable tier
(`docs/SIMD-GENERIC-KERNELS.md` §3.2). The decision to build const generics
after the lane types was taken on 2026-10-21.

## 1. The language

### 1.1 Declaring a const parameter

A const parameter is `const NAME: i32` in a generic parameter list. It can
go in the list of a function, a struct, an enum, an impl or a trait, in any
position:

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

A const argument is an integer literal or a const parameter in scope:
`Ring[8, string]`, `Vec[2, f64]`, `Vec[N, T]`. An expression such as
`N + 1` or `2 * N` is refused (§1.6). Arithmetic on const arguments needs the
checker to prove two expressions equal. Rust's first release of const
generics made the same cut for that reason.

An argument must match its parameter's kind. A type in a const position, or
an integer in a type position, is refused.

### 1.3 A const parameter in a body

In a body, `N` is a value of type `i32` and a constant. After
monomorphisation it is the literal of its instance, so the existing constant
folding sees it. `N` is not a place: an assignment to it is refused as an
assignment to a constant is.

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

The design adds these codes, numbered from the next free one when the slice
that first reports each lands:

- A const argument that is not an integer literal or a const parameter.
- An argument of the wrong kind: a type for a const parameter, or an integer
  for a type parameter.
- A const parameter used as a place.
- A `Vec[N, T]` with no lane type on the portable tier (§2.2).

A const argument that cannot be inferred reports E040, as a type argument
does.

## 2. `Vec[N, T]`

### 2.1 The spellings

`Vec` is a builtin type constructor with one const parameter and one type
parameter. `Vec[2, f64]` is `f64x2` and `Vec[4, f32]` is `f32x4`. Each pair
is one type with two spellings. A diagnostic prints the spelling the program
wrote. The lane builtins and the `Lanes` surface do not change.

### 2.2 Which pairs exist

The portable tier has a lane type only where `N` lanes of `T` fill 128 bits
and every target has the instructions: `Vec[2, f64]` and `Vec[4, f32]`.
Any other pair is refused where it is written with concrete arguments. A
generic `Vec[N, T]` is checked at each instance. `i32x4`, `i64x2` and `u8x16`
join the table when their lane types land, through the same rule.

### 2.3 What it gives a kernel

A kernel can name its lane count in its signature, and step through an array
by `N` in its body:

```fern
function scaled[const N: i32](own xs: f64[], k: Vec[N, f64]): f64[]
```

Today the same kernel takes a type parameter `T: Lanes` and reads `T.LANES`.
`T.LANES` stays, and `Vec[N, T].LANES` is `N`. A lane index stays an integer
literal (E038), so a body cannot read lane `i` for a variable `i`.

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

- **CG1.** `const N: i32` in a generic parameter list, and a const argument
  in a type application and at a call site. This goes into both front ends,
  the printers and the grammar. Both compilers learn type identity,
  inference, instance names and `N` as a value. The gate is a generic struct
  and function over `N` on every engine, and a test for each new diagnostic.

- **CG2.** `Vec[N, T]` as the second spelling of the lane types, and
  inference of `N` and `T` from a lane type. The gate is a kernel over
  `Vec[N, T]` at both widths on every engine.

- **CG3.** `std/simd` writes its kernels over `Vec[N, T]` where a lane count
  is named.

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
