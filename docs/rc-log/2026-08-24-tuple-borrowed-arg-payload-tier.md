# Borrowed call-args in the rc-tuple escape scan — the TUPB payload tier

The alias_param audit's recorded lead: `tuple_mixed__*__alias_param` leaked
identically with the callee's alias REMOVED, so the taint was never the alias
— the rc-tuple payload-escape scan (`rctuple_esc_expr`) flagged ANY call
carrying the tuple as a payload escape, and a caller's `round(keep, i)` with a
purely-reading callee cost `keep` its whole `TUPRC:`/`TUPRCS:` deep reclaim
(2/0/80 per 100 rounds; native sweeps it clean).

## The category error the review caught before it shipped

v1 forgave a bare-ident arg when the plain borrowable registry said the param
was borrowable. Both adversarial skeptics proved that unsound at runtime:
`function get(src: (i32, i32[])): i32[] { return src.1; }` keys
box-borrowable — `borrowable_params_of` only proves the callee never retains
or consumes the tuple BOX — while the caller's TUPRCS deep free walks every
rc position and frees the element `get` just handed out. Sanitizer exit 124
and `__rc_underflow_count` exit 99, on both the TUPRC caller and the
TUPELEMOK literal-init shape; the census leg is blind to it (the double free
balances). Box-level borrowability must never license a payload-level
reclaim — the same line native draws between `inferParamEscapes` and
`taintedReachesSlot`.

## The landed design: a second tier, not a wider flag

`tuple_payload_borrow_flags` computes a per-param "TUPB:" entry riding the
same bucketed registry under a prefixed key: flag '1' iff the box flag is
already '1', the param is tuple-typed, AND `rctuple_payload_escapes_alias`
proves no PAYLOAD of the param escapes the callee's body. The scan's
ExprCall arm forgives a DIRECT bare-ident arg only through
`param_is_borrowable(borrowable, "TUPB:" + callee, i)`.

Two properties keep the interproc fixpoint sound: the flags are computed
**registry-independent** (the payload walk runs with an empty borrowable
list, so an onward pass of the param inside the callee is an escape) and
**structs-blind** (no struct table in hand; a field read is a conservative
escape). Re-putting an iteration-invariant entry each round cannot perturb
the monotone-decreasing convergence argument.

## Measured (x86-64 probes, census + underflow guard)

| probe | shape | before | after |
|---|---|---|---|
| B | loop `round(keep, i)`, reading callee | 2/0/80 | 2/2/0 clean |
| C | single call, no loop | leak | clean |
| D | scalar tuple (`TUP:` class) | leak | clean |
| E | `get` returns `src.1`, literal-init caller | leak | leak 2/1/40 (refused — granting it was the UAF) |
| F | call-producer tuple into `get` | leak | 2/2/0 clean |

E stays a safe leak by design: TUPB flag 0 refuses the deep reclaim; native
is clean there via its dup-at-extract convention (retain the element as it
leaves the box), which is a separate port. F is clean through layering
alone: the payload tier refuses the element kinds, the box tier still frees
the box, and the extracted element rides out to main's `is_arr` slot sweep.

Witness cells: `tuple_mixed__elemret__payload_refused` (the refusal, pinned),
`tuple_mixed__elemret__box_tier_only` (layering), and
`tuple_mixed__fnscope__borrowed_arg` (the granted borrow) in the leak matrix.

## Next lead

The elemret floor (probe E) closes with native's dup-at-extract, not with
any widening of TUPB — an element that leaves the box gets its own retain,
after which the deep reclaim is unconditionally sound. That is the same
`countedSinkSource`/`paramCountedRetain` port the alias_param audit already
points at.
