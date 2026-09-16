# 2026-09-16 — a clone with a stranded sibling

`clone_bg` substitutes a generic's BOUNDED type parameters. The unbounded ones
are erased, and `parse_func_decl` has a promotion loop that converts an erased
var into a bounded one wherever erasure would be wrong — clause (c) for a
bare-scalar param feeding a builtin container, clause (c-arr) for a bare array
param (#6238: an erased element width made wasm32 copy an 8-byte-element array
at a 4-byte stride), clause (c′) for the two-var `Result[T, E]` shape.

Clause (c)'s comment names the failure the guards exist to prevent:

> promoting ONE var of a multi-param generic leaves the sibling erased in the
> clone (`scan[T, A]` — promoting A binds A but keeps T erased, a malformed
> `scan__i32` with `xs: T[]` that crashes)

`filter[T, I: Iterator[T]](it: I, keep: (T) => boolean): T[]` reaches that state
from the other direction. `I` carries a DECLARED bound, so the clone happens
whatever the promotion loop decides; `T` lives inside `keep`'s ARGUMENT, which
`token_at_paren_depth0` deliberately does not reach — its own comment says a
token inside `(`…`)` is not something `bind_unify` can recover. No clause fires,
and `__fn_iter__filter__iter__ArrayIter__f64` is the `scan__i32` that comment
describes: a clone with a live type variable in it.

The residual `T` then reads as pointer-shaped. On wasm32 `fn_sig_of` produces
the arity-keyed all-i32 `$fn2` while the call pushes an `f64.load`, and wasmtime
rejects the module (#9488). Native monomorphises both parameters —
`__fn_iter__filter__f64__iter__ArrayIter__f64` — so this is a self-host
divergence, not a language one.

**Clause (c-fn)** is the fn-param sibling of (c) and (c-arr): an erased var
promotes when the declaration carries a declared bound (so the clone happens
regardless) and EVERY erased var is reachable through a fn param, so promoting
the set strands nothing — the same argument clause (c′) makes. `map[T, U, I]`
needs exactly that: `T` from the lambda's argument, `U` from its result.

**The binding half.** A promoted var that cannot be bound produces an empty
instantiation key and no clone at all, so the clause is only half the work:
`infer_inst` gained the fn-param ARGUMENT-position clause `full_ret_binds` has
carried since #7765. That is the same pair as the checker fix one PR earlier,
where `gc_bind_param` gained the callable descent `type_from_ref_subst` already
had.

`sort_key[T, K: cmp.Ord](arr: T[], key: (T) => K): T[]` was a second instance,
and `call_ret_type`'s own comment had been naming it as the partially-erased
example all along.

Affected: ten declarations, the `core/iter` combinators taking a lambda plus
`sort_key`. Nothing in the compiler's own sources matches the clause.

## The binding a promoted var then REQUIRES

Promotion makes a call site that could not bind the var fail outright: the key
comes out empty and the template is dropped. That turned a working program into
a compile error for a predicate held in a LOCAL —

```fern
var keep = (x: f64): boolean => { return x > 3.0; };
var big = iter.filter(iter.of(xs), keep);
```

— because the env bound `keep` to the coarse `fn` tag (or, unannotated, to
nothing at all), and `lambda_param_types_of` had nothing to read. `mono_stmt`
now binds a fn-typed local through `fn_tag_spelling`, the same canonical
callable spelling `FuncSig.param_type_names` carries, and `mono_infer` gives a
lambda its own shape rather than "". The shape is now correct on wasm too,
where the stranded `T` had it invalid before.

`generic_fnarg_typevar` gains an `env_lambda` position for it.

## What this unblocks

`conformance/cases/generic_fnarg_typevar` gains the `floats` position the
previous PR had to withhold: it exposes #9485 where the existing `strings`
position cannot, a string element being pointer-width either way, and it runs on
the self-host wasm leg that #9488 was breaking. Expected exit 48 → 107.

## The one shape left open (#9496)

`flat_map[T, U, I](it: I, f: (T) => U[])` spells its lambda's result as a
CONTAINER of the var, which the result-position predicate matches exactly rather
than by mention. Relaxing that is one word, and the clone does come out fully
substituted — and the module is still invalid, with the mismatch REVERSED
(`expected f64, found i32`): the signature is right and a further layer pushes
the wrong width. Trading one invalid module for a differently invalid one is not
a fix, so the gate stays exact and `flat_map` is left exactly as it is on main,
with the reversed error recorded as #9496's starting point.
