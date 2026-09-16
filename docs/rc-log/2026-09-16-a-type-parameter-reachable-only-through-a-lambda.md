# 2026-09-16 — a type parameter reachable only through a lambda

`filter[T, I: Iterator[T]](it: I, keep: (T) => boolean): T[]` reaches `T` in no
parameter type of its own. `it: I` binds `I`; `T` is inside `keep`'s argument.
`clone_bg` substitutes the BOUNDED type parameters only, so the instance at
`I = ArrayIter[i32]` still declares `T[]` as its return and reports
`type_param_count` 0. The caller's binding kept an unresolved element type and
the semantic lowering refused it: `unresolved type of binding $binding$2$evens`.

Three things were in the way, and all three had to go.

**The spelling.** `parse_decl_type` splits a function-typed declaration into a
coarse `fn` / `fn[]` tag and its result and parameter sidecars, and
`FuncSig.param_type_names` recorded the tag. `parse_type_ref("fn")` is an opaque
name, so there was nothing for a structural reader to descend into.
`parser.fn_tag_spelling` is the inverse of that split, and `param_type_names`
and `ret_type_name` now carry what it rebuilds. A tag with no recorded result
vouches for no signature and stays itself.

**The unification.** `gc_bind_param` descended an array and a tuple spelling but
not a callable — the asymmetry is visible against `type_from_ref_subst`, which
has handled `ref_is_fn_value` all along. It now unifies a callable's parameters
and result against the argument's `TypeFunc`, which is where `T` is. The
monomorphiser's `full_ret_binds` grew the same argument-position clause in
#7765; this is its checker twin.

**The guard.** The inference block asked for `type_param_count > 0`, which the
instance no longer has. A declared type parameter was never the precondition —
a return spelling that resolves to nothing is, and `type_contains_unknown` on
the declared result already says exactly that. A spelling that DOES resolve
binds nothing, so a call with no variable in it reaches the fallthrough as
before.

Whole modules produced: 439 → 442. `unresolved type of binding` drops from 16
refusals to 9, and `conformance/cases/generic_fnarg_typevar` and
`conformance/cases/erased_typevar_ret` go from producing nothing to producing
whole (73 of 73 and 65 of 65).

## The f64 read this was hiding (#9485)

Not the semantic lowering's, and not a refusal: the AST lowering answered
wrongly. With the element type unresolved, `type_to_irtag` yields "" and the
backends fall back to an untyped 4-byte read — so every element of an `f64[]`
handed back by `iter.filter` came out 0 while its length was right. Native was
correct throughout.

`conformance/cases/generic_fnarg_typevar` had a `strings` position for exactly
the "not i32-shaped" worry and could not catch it: a string element is
pointer-width either way.

The f64 position belongs in that case and is not there, because the case runs on
the self-host wasm leg and the self-host wasm emitter is separately wrong on this
shape (#9488): the residual `T` also reaches the funcref type a `call_indirect`
dispatches through and the push helper an `append` selects, so it emits a module
wasmtime rejects. Pre-existing, and it reproduces on main. Deriving the funcref
type from what the call PUSHES fixes that half and moves the failure four bytes
along to the array push — each consumer patched uncovers the next, which says the
residual parameter is the defect rather than any one reader of it, and the fix
belongs at the instantiation. So the shape is pinned in
`internal/e2eselfhost/self_host_generic_fnarg_width_test.go` on x86-64 against
the native compiler, and the conformance position lands with #9488.

## What the fix surfaced underneath

An `(f64) => boolean` lambda is still refused, now as `function signature slot`
rather than an unresolved binding: `ssasem.narrow_word` admits no float in a
funcref signature, because the stack IR's untagged indirect call describes every
slot as one word. That is the vocabulary's stated limit, not a regression — the
i32 shape of the same program produces whole.
