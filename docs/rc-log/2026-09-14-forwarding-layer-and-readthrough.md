# The forwarding layer, and the read-through floor

The residue #9203 still carried after the counted handback (#9212) and the
unannotated credit (#9226), found by writing the shapes out and reading the
census rather than by any suite going red. #9235 tracked both halves.

## The split that located it

One forwarding LAYER between the caller and a strict-fresh producer. The layer
is the only thing that varies, and it decided whether the receiver's buffer
came back:

| layer | census |
| --- | --- |
| free function, `fwdfree(b, 1)` | 3 allocs / 3 frees / 0 live |
| method, `return a.id_or_make(k)` | 3 / 2 / 48 |
| method, with its own `if (k <= 0) { return a; }` too | 3 / 2 / 48 |
| method forwarding to a plain recv-borrow producer | 3 / 2 / 48 |

Native reads 3 / 3 / 0 for every one of them, so this was a port gap and not a
design question. The direct call with no layer at all balances, which is what
says the layer is the variable.

Two readings are ruled out by that table, and both were the first guesses. It
is not the inner callee returning its receiver: row 4 forwards to a producer
with no identity return. It is not the absence of a bare `return a`: row 3 has
one.

## Cause: one pass, and an empty nomove list

Dumping the registries for the repro settles it in two lines. `Big.fwd` is in
`cnt_struct_ret_fns`, and `recv_borrow_fns` carries **no row for it at all**,
where `Big.id_or_make` carries both `CNTRECVRET:` and `RECVIDENT:`.

`recv_borrow_fns_of` answered every method from ONE pass with an empty nomove
list, and `moves_fields_expr` marks a method RECEIVER unconditionally when the
callee is not in that list. So `return a.id_or_make(k)` marked `a` as moving a
field, which gated off both tiers at once. The caller read no exemption,
dropped the receiver box-only, and the count the inner callee's `var mag =
a.mag` had added was never given back.

Adding a bare `return a` to the layer earns `CNTRECVRET:` and still leaks:
`RECVIDENT:` is the row the caller's exemption list actually reads.

## The registry is a least fixpoint

Each pass answers from the previous pass's rows, so a method forwarding into
one already proven learns from it. The nomove list is the plain key plus the
`RECVIDENT:` tier — the same pair the CALLER side already trusts for this
question, which is what makes the feedback consistent rather than novel. The
passes only add rows, so the iteration is monotone and stops when a pass adds
none.

A forwarding return also earns the recvret row: the result MAY be the callee's
handback of the receiver's own box, exactly as a bare `return self` makes it.
It stays OUT of the plain key for that same reason — `expr_unsafe_for` reads a
receiver position as a borrow, so the plain key would claim an escape-free body
this one does not have. Getting that wrong is a use-after-free rather than a
leak, which is why the tier matters more than the row count.

## The read-through floor lifts without the proof it was waiting for

The read-through release was the bare box dec, and its comment named the
reason: a counted member returns a fresh literal on one path and the receiver's
own box on the other, and a fresh return's field values are not proven owned,
so walking them could free what a caller still holds. It cost one buffer per
read — `b.id_or_make(0).mag.len() + b.id_or_make(1).mag.len()` read 3 / 2 / 48.

The callee-side proof is not needed, because the two paths do not have to be
told apart at compile time. `emit_struct_field_drops_gated` walks only behind
`__fern_rc_is_unique`: on the identity path the receiver still holds the box,
so the walk does not fire and the receiver's own release does it later; on the
fresh path the box is a strict-fresh literal this frame solely owns. That is
the discriminator the argument-temp and exit-sweep releases already stand on.

## A correction to the previous entry

`2026-09-14-counted-struct-handback.md` lists `b.mul_pow10(1)` at 400 / 400
after the field-read half. Measured on that tree, the shape written as
`if (k <= 0) { return a; } return a.id_or_make(k);` did not balance — it is
exactly the method-forwarding row above and read 3 / 2 / 48 for one round. The
row was right about the direct producer and wrong about the forwarding
spelling `core/bigint` actually uses. It balances now.

## Gates

Five rows in `TestSelfHostWithCowIR{X86_64,Arm64,Wasm}`:
`struct-handback-fwd-method`, `-mixed`, `-identity`, `-two-layers` and
`struct-handback-readthrough`. The identity rows matter most: there the result
IS the receiver's box, so an over-release reads 99 rather than a leak, and
`-two-layers` can only pass if the iteration is a fixpoint rather than one
lookahead.

Also green: the cow tables on x86-64 and wasm, `TestSelfHostRecvBorrowDeepDrop`,
`TestSelfHostBorrowedStructParamReturn`, `TestSelfHostTupleHandbackReclaim`,
and the reclaim / leak-matrix / alloc-differential / snapshot / fresh-ret
families; the whole-compiler emit-all fixpoint, unchanged at ~395 s; the
complexity ratchet; `make fmt-check`.
