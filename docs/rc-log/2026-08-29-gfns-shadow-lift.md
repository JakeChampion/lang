# The no-capture lift forgave a shadowed module-function name

`lambda_has_no_captures` decides whether a lambda can be hoisted bodily to a
top-level `__lam_N`. It walks the body's free idents and forgives one that names
a module function, on the reasoning that it is a call target rather than a
capture:

```fern
if (util.index_of_str(bound, u) < 0 && util.index_of_str(gfns, u) < 0
        && u != "None" && u != "true" && u != "false") {
    return false;
}
```

There is no check that the name still MEANS the module function. This is the
exclusion #6191 removed from the sibling `lambda_captures`, whose own doc says
why:

> a name that is BOTH an enclosing local and a module function is shadowed, so
> it must be captured, and the gfns exclusion said the opposite. The lambda then
> reported no captures at all, took the no-capture lift, and resolved the name
> against the module function table inside the trampoline — a silent call to an
> unrelated top-level function of the same name.

The sibling predicate never got the same treatment.

## Finding a shape that reaches it took a census, not more guessing

Three hand-written probes measured clean. Each looked right on paper and none of
them reached the predicate: a lambda in a call-argument position went down the
escaping-closure `$clo` path, and a capture-free one was inlined without any
`__lam_N` at all.

So the predicate was instrumented instead — one `eprint` on the branch where
`gfns` is what forgives a free ident — and the whole corpus compiled through it:

| corpus | firings |
|---|---|
| `conformance/cases` (514 fixtures) | **13**, across 5 fixtures |
| the self-host compiler's own sources (~190k lines) | **0** |

The 13 name `id`, `widen`, `pick` and two monomorphised array methods, and the
five fixtures are all value-block width cases. That named the shape: a
value-position `if` desugars to a zero-argument IIFE, whose body calls a module
function as a free ident. That IIFE is exactly what the no-capture lift hoists.

Contrast the `clo_rc` census on this issue, which came back 0 everywhere and
retired the family. Same instrument, opposite verdict — which is the argument
for running it rather than reasoning about reachability.

## The measurement

```fern
function widen(v: i64): i64 { return v; }
function main(): i32 {
    var widen: i64 = 7000000000i64;                       // shadows the module fn
    var xs: i64[] = [1i64, (if (true) { widen } else { 2i64 }), 3i64];
    if (xs[1] != 7000000000i64) { return 1; }
    return 0;
}
```

| | interp | self-host, before | after |
|---|---|---|---|
| shadowed | 0 | **1** | 0 |
| the rename control (`w`) | 0 | 0 | 0 |

The control renames the local and changes nothing else. It also measures **zero**
forgiveness firings, which is what says the forgiveness is the mechanism rather
than a coincidence of the arithmetic.

## The fix filters the LIST, not the predicate

`gfns_visible_in(fd, gfns)` drops from the module-function list every name the
enclosing function also binds — receiver, parameters, and every body binder via
`astwalk.collect_bound_stmt`.

Filtering once per function rather than threading `fd` down through
`lift_stmts` / `lift_stmt` / `lift_call_arg` / `lift_call_callee` is what makes
this a four-line change with no signature churn, and it fixes every consumer at
once. There are three: the step-2 lift in `lift_lambdas_view`, and two in
`closure_ret_fns_of`, which asks the same predicate to decide whether a function
RETURNS a closure — over-forgiving there means a closure-returning factory is
not registered as one.

The forgiveness itself is load-bearing and stays: a genuine call to an
unshadowed module function from inside a value-block IIFE must still lift, or it
becomes a capture the lift cannot type. The conformance case asserts that limb
too, so a fix that simply deleted the exclusion would fail it.

Gate: `conformance/cases/value_block_shadows_module_fn`, which exits 1 on the
pre-fix compiler and 0 after — native, interp and all three self-host backends
by construction.
