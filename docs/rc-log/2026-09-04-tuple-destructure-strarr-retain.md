# 2026-09-04 — the tuple destructure's retain reaches `string[]`

`var (a, b) = p` over `var p: (i32, string[]) = …` extracted the element
pointer with no retain while marking `b`'s slot one the scope-exit sweep
releases. Where `p` also carried the "TUPRCS:" sweep credit the buffer was
decremented twice — the shallow dec at `b`'s last use frees it, the tuple's
`__fern_str_arr_free` then underflows into the quarantined block.

Dup-at-extract (#7682) fixed exactly this for `is_leaksafe_array_field`. It
left `string[]` out on the reasoning that a `string[]` position only ever
LEAKS, so a retain would deepen the leak. That was true of the element forms
measured at the time and not of the class: the sweep credit's admission
(`tuple_arg_payload_droppable`) has always taken a `string[]` position built
from string LITERALS, and `9b4423842` widened it to registry-fresh producers,
which is where CI saw it.

## Measured — 100 rounds, x86-64, one fixed native compiler

`function round(i) { var p: (i32, string[]) = (i, ELEMS); var (a, b) = p; return a + b.len(); }`

| ELEMS | self_host `00eacd3f3` | self_host `9b4423842` | with the retain |
|---|---|---|---|
| `[w("x"), w("y")]` (registry-fresh) | exit 9, 400/100 10400 | **exit 99**, 400/200 6400 | exit 9, 400/400 **0** |
| `["x", "y"]` (literals) | **exit 99**, 200/200 0 | **exit 99**, 200/200 0 | exit 9, 200/200 **0** |
| `xs` (bare ident) | **exit 99**, 200/200 0 | **exit 99**, 200/200 0 | exit 9, 200/200 **0** |

exit 99 is `__rc_underflow()`; `FERN_SANITIZE=1` reports
`use-after-free (touched a quarantined block)` on the same three.

The literal and bare-ident rows are the ones that matter for placement: both
over-release on `00eacd3f3`, so a fix scoped to the registry admission
`9b4423842` widened would have left two shapes corrupting memory and only
moved the pinned row back to green.

## Why the retain balances rather than deepens

`__fern_str_arr_free` is rc-gated: it walks the element boxes only at rc 1.
The binding's shallow `__fern_arr_dec` is a precise drop at last use, so it
runs first.

```
construction 1 → extract retain 2 → binding's dec 1 → tuple's deep drop 0
```

The element walk therefore still happens, at the tuple's drop instead of never.
That is why the registry-fresh row lands at 400/400 rather than at the
frees=100 leak it was pinned to.

## The residual

The MOVE path is the one position where the retain is not given back:

```
function get(i: i32): string[] { var p: (i32, string[]) = (i, [w("x"), w("y")]); var (a, b) = p; return b; }
```

`b`'s slot sweep is elided, so the tuple's drop finds rc 2, decs without
walking, and the caller's shallow dec frees the buffer with the element boxes
on it — 400/200, 6400 B over 100 rounds. Sound, and the alternative is what
`9b4423842` measured: 400/400 0 with exit 99, the buffer freed under a live
caller binding. `strarr_moved_out_by_return` pins the leak so closing it moves
a number.

## Scope

Struct- and enum-array elements stay outside the retain, and that is not a
deferral: `tuple_arg_payload_droppable` admits no tuple position of those
types at all, so no tuple sweep can release one. Measured `(i32, P[])`
unchanged at 400/100 12000 across all three compilers.

## A second admission the same restructuring dropped

`body_has_nonfresh_opt_payload` answered a bare string-literal payload with
its own arm:

```
var pfresh: boolean = str_local_binding_is_fresh(c.args[0]);
match (c.args[0]) { ast.ExprString(_) => { pfresh = true; }, _ => {} }
```

`9b4423842` replaced it with `opt_ctor_payload_fresh`, whose header says a
literal "is already answered by the tuple element predicates". None of them
answers one standing alone — `tuple_str_elem_fresh`, `tuple_str_elem_fresh_reg`
and `tuple_strarr_elem_fresh` each take an `ast.ExprString` only as a concat
operand or an array element, which is why `tuple_strarr_elem_fresh`'s own
element loop has to match `ExprString` separately before calling into them.

So `Some("abcd")` lost the OPTFRESH "f" flag and the call-bound release with
it, 100 rounds against the interp oracle those rows already assert:

| producer | `00eacd3f3` | `9b4423842` | with the literal readmitted |
|---|---|---|---|
| `Option[string]`, block-scoped bind | 400/400 0 | 400/0 16000 | 400/400 0 |
| `Result[string, i32]`, string Ok | 400/400 0 | 400/0 16000 | 400/400 0 |
| `Option[string]`, fn-scoped bind | 100/100 0 | 100/0 4000 | 100/100 0 |

`TestSelfHostCallBoundStrPayloadReclaimX86_64` already pinned all three; the
`Some("ab" + "cd")` row beside them stayed green throughout, which is what
isolates the loss to the literal form.
