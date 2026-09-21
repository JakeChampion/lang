# A generic call's result reaches the call site with its type var unresolved

**Status:** the two reproducers below are fixed — both answer 45 on x86-64 and
wasm, on either lowering. **The mechanism is not.** `type_to_irtag` still yields
"" for an element type the checker left unresolved, and the backends still fall
back to an untyped 4-byte read of it, so every shape the call-site binding does
not reach still answers silent wrong values with exit 0 and no diagnostic. What
closes is one shape at a time, as `gc_bind_param` learns to unify it: an array
element, a tuple element, and (#9485) a callable's parameter. No
`unresolved type of binding` refusal remains in the corpus: an unannotated
binding of a call takes the call's own result type now, which is the one the
instance settles, so the checker never has to reach it. That closes the
REFUSALS, not the mechanism — the hazard is a shape the call-site binding does
not reach, and a binding is only one of the places a type var arrives
unresolved. #9488 is the same hazard one layer down, in the backend rather than
the checker.

#9489 is the question this raises and does not answer: whether an empty tag
should be a REFUSAL on the AST path, which would convert the whole class from a
silent 0 into a diagnostic.

## Reproducer

```fern
function enum2[T](xs: T[]): (i32, T)[] {
    var out: (i32, T)[] = [];
    var i: i32 = 0;
    while (i < xs.len()) { out = out.append((i, xs[i])); i = i + 1; }
    return out;
}
function main(): i32 {
    var xs: f64[] = [4.5];
    var ps = enum2(xs);
    return (ps[0].1 * 10.0) as i32;   // want 45; self-host x86-64 gives 255, wasm 0
}
```

`array.enumerate` at an `f64[]` is the real-world instance.

## What isolates it

| probe | shape | self-host x86-64 |
|---|---|---|
| `dup[T](xs: T[]): T[]` | generic, ARRAY return | 45 correct |
| `pk[T](xs: T[]): (i32, T)` | generic, BARE TUPLE return | 45 correct |
| `enum2[T](xs: T[]): (i32, T)[]` | generic, TUPLE-ARRAY return | **255** |
| `mk(): (i32, f64)[]` | NON-generic, tuple-array return | 45 correct |
| `enum2` with `var ps: (i32, f64)[] = …` | annotated binding | 45 correct |

Generic alone is fine. Tuple-array alone is fine. The combination is not.

The annotated form works for a different reason than it looks: the annotation
gives the slot a concrete `arrarr_elem`, so the tag is never consulted.

## Measured, not inferred

An earlier version of this writeup asserted the tag was *present and wrong*
(`"(i32, T)"`). That was inference, and it was **wrong** — the two are
indistinguishable from the symptom, because `tuple_type_elem_tag` yields `"T"`
from a wrong tag and `""` from an absent one, and neither is a width, so both
fall back to the same i32 read.

Instrumenting the `ExprIndex` arm of `expr_tuple_elem_tag` settles it:

```
h1 (generic):     PROBE ExprIndex.ty=[] n=1 array=call ty=[]
g3 (via local):   PROBE ExprIndex.ty=[] n=1 array=ident ps slot_arrelem=[]
h2 (non-generic): (no output — the tag was present, early return fired)
```

The tag is **ABSENT**, and so is the tag on the call behind it. The local's
`arrarr_elem` is empty only because it is bound from that untyped call.

## Why it is empty

`type_to_irtag` (checker.fern) serialises a `Type` to the canonical string
irlower keys on. Two of its properties combine here:

1. It has **no `TypeArray` arm** — arrays fall to `_ => ""`. That is deliberate
   and documented: `type_to_irtag`'s result is read by tag-FIRST consumers
   (`expr_is_str` / `_f64` / `_u32` / `_u64` all short-circuit their structural
   walk on any non-empty `c.ty`), so teaching it to name arrays would change what
   those four see on every array-valued call. `type_to_arrtag` exists separately
   for `ExprSlice.ty`.
2. Its `TypeTuple` arm returns `""` if **any** element fails to serialise.

The checker computes the element of `enum2(xs)` as `(i32, T)` — with `T` still a
type variable. A type var has no arm, so it serialises to `""`, so the whole
tuple serialises to `""`, so `ExprIndex.ty` is empty and lowering falls back to
the untyped 4-byte read.

So the defect is **the checker not instantiating the generic's return type at the
call site**. `enum2(xs)` with `xs: f64[]` should have element `(i32, f64)`, which
serialises fine — as the non-generic `mk()` row proves.

## Not the annotate/monomorphise ordering

`checker.annotate_module` (fern.fern:640) does run before `monomorphize_module`
(inside `parser.module_with_builtins`, fern.fern:682), and an earlier writeup
blamed that. It is not sufficient: `dup` and `pk` are annotated at exactly the
same point and both work. Ordering would predict failures that do not happen.

## The fix

`check_call_expr` binds the type args from the arguments at a generic call site
and substitutes into the return type before handing it back, so the tag never
names a type var — every consumer of the tag at once rather than the destructure
or the field read individually. Re-annotating after monomorphisation would also
have worked and is a smaller diff, but it treats the symptom: the checker would
still be returning a type with an unbound var in it for anything that asks
before that point.

`gc_bind_param` is the unification that binding runs on. Each shape a variable
can hide in has had to be taught to it separately — an array element, a tuple
element, and (#9485) a callable parameter, where `filter[T, I: Iterator[T]]`
keeps its only `T`.

## A second bug found alongside, since fixed

```fern
function mk(): (i32, f64)[] { return [(0, 4.5)]; }
function main(): i32 { var ps = mk(); var t = ps[0]; return (t.1 * 10.0) as i32; }
```

It answered 45 on interp and self-host x86-64 and **1** on self-host wasm:
NON-generic, so not the above, and it produced no output from the `ExprIndex`
probe either — binding a tuple LOCAL from an index of an unannotated array local
lost the element widths somewhere else, on wasm only. It answers 45 everywhere
now.
