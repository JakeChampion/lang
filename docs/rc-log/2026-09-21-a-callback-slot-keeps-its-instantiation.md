# 2026-09-21 — a callback slot keeps its instantiation

A generic struct whose method takes a CALLBACK over that same struct refused
the whole module on the typed path, and had done since the struct
monomorphiser existed. Seventeen self-contained lines are the whole of it:

```fern
struct Slot[T] { v: T }

function (s: Slot[T]) through[T, U](f: (Slot[T]) => Slot[U]): Slot[U] {
    return f(s);
}
```

`produced 0 of 5 declarations … the AST lowering stands`, with two roots and
three consequences:

```
FERN_SEM_IR: main: call argument type
FERN_SEM_IR: __smm_Slot_through__i32__string: call argument type
```

## A declaration carries its callable spellings on the side

`ast.ParamDecl` does not hold a parsed type. A plain parameter is a string in
`type_name`; a CALLABLE parameter sets `type_name` to the sentinel `"fn"` and
puts the real signature in two more fields — `fn_param_types`, a
comma-separated list, and `fn_ret`. `StructFieldDecl` and `FuncDecl` carry the
same pair for a field and for a callable RETURN.

`monomorphize_structs` rewrites every type spelling on a declaration to the
mangled clone name: `Slot[i32]` becomes `Slot__i32`, through `mg_ty`, which
also REGISTERS the instantiation so the clone gets built. `ms_func` ran it over
each parameter's `type_name`, the return, the receiver, and the body. It
rebuilt each `ParamDecl` field by field — and copied `fn_ret` and
`fn_param_types` through verbatim.

So the clone of `through` said `Slot__i32` in every position but the one that
mattered, where it still said `Slot[i32]`. Nothing resolves that spelling to
the clone: the generic `Slot` was dropped with its type parameters, so the
name resolves to the bare struct and the argument falls away. `f` typed as
`(Slot) => …`, the argument was a real `Slot__i32`, and `indirect_call`
refused `call argument type` — taking `main` with it, since `main` hands the
same callback in.

## Every site, because it is one rule

Seven places rewrite a declaration in that pass, and all seven skipped the
pair. `ms_func`'s parameters and its own callable return; `ms_expr`'s lambda
parameters; `ms_stmt`'s `var`; `clone_struct_method`'s parameters and callable
return; the struct fields of a non-generic struct; and the fields of a generic
struct's clone. `mg_ty_list` is `mg_ty` over the comma list, and
`to_concrete_struct_ty_list` the same for the clone path, which mangles
against a known base rather than through the accumulator.

Threading the accumulator matters as much as the spelling: a generic struct
named ONLY inside a callable's signature is now registered, so its clone is
built rather than dangling.

`mg_ty` also learned the `own ` prefix, which `subst_ty` beside it already
handled — a callable's parameter list is the one place a consuming slot is
written into a type spelling, so it is the one place `mg_ty` would meet it.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`. The repro goes 0 of 5 to
**5 of 5**, both legs answering `big/lam` and exiting 3. The typed leg
reclaims it whole — `allocs=7 frees=7 live_bytes=0` — where the AST leg
strands **176 bytes in 5 blocks**.

**The corpus census does not move**: 841 programs whole and 77,287
declarations of 78,504 before and after, over the same 864 seeds against
frozen binaries. No file regresses either.

That is worth stating plainly rather than dressing up. The corpus has exactly
one program of this shape — `examples/tests/ndarray_test`, whose
`map_rank[T, U](k: i32, f: (NdArray[T]) => NdArray[U])` is `through` with more
arguments — and it produces 0 of its 254 declarations both before and after,
because a SECOND root holds it: the `calls a function value of N arguments, a
type the AST lowering builds a value of` mixing rule in `semlower.fern`. This
change clears the first of the two. A census counting declarations cannot show
that, and the 17-line repro can.

## Tests

`a-callback-slot-keeps-its-instantiation` — 5 of 5, `noLeak`, all four
targets, differential against the AST leg. Both callback shapes are in it
deliberately: a named function and a lambda reach the slot by different
routes, and `U = string` over `T = i32` means a spelling that lost its
argument cannot pass as the receiver's own.

That the row can FAIL was checked rather than assumed: with `parser.fern`
reverted and the rest of the branch in place it goes red on all four targets,
on the produced count AND on `noLeak`.
