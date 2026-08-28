# The OPTSTR payload proof learns the string-fresh registry

Flips `opt_str__callarg__read` — the "plan grants but the sweep stays refused
elsewhere in the family" floor the 08-24 plan-routing entry pinned at 300/0
per 100 rounds.

## The asymmetry

The cell's `var o: Option[string] = Some(mk("abc"))` needs four conjuncts for
its `OPTSTR:` credit; three held. The plan side (`opt_unmatched_esc_ok`,
routed 08-24) grants the call-arg escape; the refusal was
`unmatched_optstr_init_is_fresh`'s Some/Ok payload arm asking only the
syntactic `str_local_binding_is_fresh`, which reads a producer CALL as
not-fresh. The RCENUM family answers the identical shape
(`G.Full(mkstr("x"))`) with a disjunction over `str_fresh_ret_fns_of` —
`variant_struct_payloads_fresh`'s string arm — and its whole family is clean.
OPTSTR was the one rc family whose payload proof never got the registry
disjunct.

## The fix

`unmatched_optstr_payload_is_fresh`: registry membership
(`str_call_ret_is_fresh_reg` over `str_fresh_ret_fns_of`) OR the syntactic
proof, as a named predicate (the ratchet lesson from the 08-27 str-rebind
entry), threaded through `collect_unmatched_optstr_names` from
`reclaimable_names_of`, where `str_fresh_ret` already sat in scope. No gate
loosened: the registry's own fixpoint refuses any producer that can return a
param, field, or receiver, which is exactly the alias hazard the freshness
requirement exists for (`op_opt_make` stores its payload uncounted).

## Probes

`self_host_optstr_callarg_test.go`, exits confirmed on `-interp` AND native:

| case | exit | pin |
|---|---|---|
| the cell verbatim | 14 | balanced, live 0 |
| 200 rounds same-size churn + content read-back of a kept string | 17 | balanced, live 0 |
| the cell with `FERN_SELFHOST_RC_PLAN=0` | 14 | must stay a LEAK — the off-plan gate did not widen |

Each census case re-runs under `FERN_SANITIZE=1` asserting the same exit and
no over-release / use-after-free report. Non-vacuity by revert: the cell
reads 300 allocs / 0 frees / 7,200 live on the parent compiler and fails the
balance assertion.

The three standing refusals in `self_host_unmatched_optstr_reclaim_test.go`
(`aliased_producer_payload` — `wrap(s) { return Some(s); }` — plus
`inline_ctor_over_a_bare_local` and `box_read_after_the_loop`) hold
unchanged: releasing those is a dangle, not a fix.

## Gates

Both leak-matrix dumps (x86-64 + arm64): exactly this row moved, pins and
notes updated in both files. Stage-2 arm64 fixpoint, emit-all fixpoints,
construction-retain + container-sink matrices, the tuple/optstr probe suites,
feature census, complexity ratchet, `make check-sources`, cliff-bench —
recorded in the PR.

## What remains

Two actionable cells: `tuple_mixed__elemret__payload_refused` (closes with
native's dup-at-extract, not a TUPB widening) and nothing else outside the
two alias-consumed-by-match denials whose notes call the denial sound. The
`Result[_, string]` Err-payload strand stays separately scoped out.
