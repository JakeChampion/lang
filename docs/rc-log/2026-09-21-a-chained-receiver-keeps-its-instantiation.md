# 2026-09-21 — a chained receiver keeps its instantiation

`examples/tests/ndarray_test` did not compile on the self-host. Not "fell back
to the AST lowering" — did not build, on either leg:

```
FERN_STRICT_IR: main (call to unknown symbol ndarray__NdArray__i32.map)
error: module is not IR-eligible; the AST emitter is no longer reachable
```

It passes 17 of 17 on native. It was one of only two corpus programs failing to
compile for a reason that is not a deliberate diagnostic case, and the census
reported it as `produced 0 of 246 … the AST lowering stands`, which reads like
an ordinary refusal and is not one. A census that counts declarations cannot
see the difference; only the exit code can.

Six lines reproduce it:

```fern
var m: ndarray.NdArray[i32] = a.transpose().map((x: i32): i32 => x * 2);
```

Binding the receiver to a name first compiles, and produces whole. So the
trigger is precisely the receiver being a CALL RESULT.

## Root one: the receiver's instantiation never reached the return

`map[T, U]` declares type parameters of its own, so
`register_struct_method_generics` folds it into the free generic
`__smm_<Base>_map` with the receiver as argument 0 and **drops the method**.
`mono_expr` rewrites `recv.map(f)` onto the fold, gated on the receiver's
inferred spelling being a bracketed instantiation.

`recv_method_ret_of` answered with the chained method's DECLARED return.
`transpose` is `(a: NdArray[T]) transpose(): NdArray[T]`, so it handed back
`NdArray[T]` — the receiver's own `[i32]`, which it is holding in `rvt` on the
line above, never substituted in. `looks_type_var` rejects only a BARE
variable, so a `T` nested inside brackets passed through. The gate failed, the
call kept a method spelling whose declaration had been dropped, and the emitter
had no such symbol.

`generic_recv_ret_of` matches the declared receiver against the inferred one
and carries that substitution into the return. It reuses `ref_bind`, added one
change earlier for bound-driven instantiation — the same positional match on
type spellings, at the same level the monomorphiser works at.

## Root two, one layer in

Fixing that moved the bail rather than clearing it:

```
FERN_STRICT_IR: test_map_rank_maps_every_cell (call to unknown symbol ndarray__NdArray__i32.map_rank)
```

`map_rank[T, U](k: i32, f: (NdArray[T]) => NdArray[U])` pins U only inside the
callback's BRACKETED return. `infer_inst` recovers a variable from a fn
parameter's return, but gated it on the return BEING one:

```fern
if (fd.params[i].type_name == "fn" && tp_index(fd.params[i].fn_ret, fd.type_params) >= 0) {
```

`bind_unify` already descends into a bracketed spelling — it has the arm for it
— so only the gate was wrong. `spelling_mentions_tparam` asks the structural
question instead.

The contrast is what identifies it. With root one fixed:

| call | result |
|---|---|
| `a.map(double)` — named fn, bare `T` → `U` | 18 of 18, answers 8 |
| `a.map((x: i32): i32 => x * 2)` — lambda, bare | 20 of 20, answers 8 |
| `a.map_rank(1, sum_cell)` — `(NdArray[T]) => NdArray[U]` | did not build |

So it is not the named callback and not the fold; it is a type argument nested
one level inside the callback's spelling.

## What this is worth, stated exactly

`ndarray_test` compiles and passes 17 of 17, matching native. The corpus goes
from 72 programs failing to compile to 71.

**The typed-path census does not move at all**: 841 programs whole before and
after, 77,287 declarations of 78,496 before and after, measured against frozen
binaries over the same 864 seeds. `ndarray_test`'s own total rises 246 → 254 as
more of it becomes reachable, and it still produces **0** of them.

That is not a disappointment, it is the shape of the thing: what held the file
was a compile failure, and what holds its declarations is the
`calls a function value of N arguments, a type the AST lowering builds a value
of` mixing rule in `semlower.fern`, which is a different leaf with a different
root. A change that makes a file build is worth landing on its own terms; it is
not worth writing up as production it did not deliver.

## Tests

`a-chained-receiver-keeps-its-instantiation` (20 of 20, `noLeak` — the typed leg
reclaims it whole at `live_bytes=0` where the AST leg strands 640) and
`a-callbacks-return-pins-the-methods-own-variable`.

The second is `atLeast: 0` with no `noLeak`, deliberately: the typed path
refuses all 28 of its declarations, and both legs hold the same 1232 bytes
because on that program the typed leg IS the AST leg. What it pins is that the
module compiles and answers — which before the fix the harness cannot even
reach, since the compile itself fails.

Each row was checked to fail for its OWN root, not merely for the pair:

| reverted | chained-receiver row | map_rank row |
|---|---|---|
| root two only | PASS | FAIL |
| root one only | FAIL | PASS |
