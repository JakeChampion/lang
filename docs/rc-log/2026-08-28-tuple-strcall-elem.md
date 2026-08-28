# The rc-tuple credit learns the string-fresh registry at a call element

Closes #7374: `var v: (i32, string) = (1, w("p"))` released NOTHING — not the
string box, not the tuple box — at 72 B/round unbounded (600/0/14400 over 200
rounds against native's 200/200/0), because the element freshness proof was
syntactic-only and `.to_string()` was the one call form it knew.

## The asymmetry

The issue's own probe located it exactly: `(1, i.to_string())` was clean
end-to-end under the same compiler, so the whole credit-and-free path already
handled a call element — only the freshness verdict differed. The earlier
threading attempt (recorded on #7374) failed because the verdict is consulted
at TWO gates — `tuple_lit_has_rc_child` (the rc-child count) and
`tuple_arg_payload_retained` (the blind-sweep soundness gate) — and passing a
placeholder registry to the second while measuring the first measured nothing.

## The fix

`tuple_str_elem_fresh_reg`: registry membership (`str_fresh_ret_fns_of`
bare-name entries) OR the syntactic proof, as a named predicate (the OPTSTR
entry's lesson, one entry up), with concat operands recursing through the
widened form. Threaded to both gates from `reclaimable_names_of`, where
`str_fresh_ret` already sat in scope: `tuple_lit_rc_reclaimable` /
`tuple_lit_has_rc_child`, `tuple_arg_payload_retained` /
`tuple_arg_payload_droppable`'s string arm, the two collectors, the
`all_assigns_fresh_rc_tuple` rebind walker, and `reclaimable_rc_tuple`. The
emit side (`emit_tuple_child_drops`' binary/call arms,
`tuple_union_arg_freefn`'s catch-all) reads the same registry off
`LowerState.sigs`, so credit and release stay one verdict.

**The sole-owner flavour keeps the syntactic admission.**
`tuple_arg_payload_fresh` (an Option's tuple payload, an array-of-tuples
element — sites that free the payload without freeing a box the same
statement built) passes an explicitly-empty registry with the scope split
recorded at the call site and on #7374. Widening each is its own measured
increment; this entry's is the rc-tuple credit pair.

No gate loosened past the registry's fixpoint: a producer that can return a
param, field, or receiver never registers, which is exactly the alias hazard
the freshness requirement exists for.

## Probes

`self_host_tuple_strcall_elem_test.go`, exits confirmed against native:

| case | exit | pin |
|---|---|---|
| the issue's repro verbatim | 68 | balanced, live 0 (was 600/0/14400) |
| 200-round churn + content read-back of a kept string | 17 | balanced, live 0 |
| rebind flavour (`v = (j, w("qr"))` in a loop) | 11 | balanced, live 0 — assign-site deep drop + sweep |
| `id(q)` aliased-producer element | 53 | must stay a LEAK, frees=0 — release here is the #4294 shape |

Each re-runs under `FERN_SANITIZE=1` asserting the same exit and no
over-release / use-after-free.

## What remains under #7374's shapes

The producer-return form (`function mk(): (i32, string) { var t = (1, w("p")); return t; }`
consumed by a caller binding) moved from 600/0/14400 to 600/200/6400 — the
binding side now releases; the return-transfer side is the second gap the
issue predicted and is not this entry's. The string-ARRAY element
(`tuple_strarr_elem_fresh`) and the sole-owner flavour's families (OPTTUP,
arr-of-tuples) keep their syntactic admissions, each a measured increment
away.
