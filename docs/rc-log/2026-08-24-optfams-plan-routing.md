# OPTARR / OPTSTR / OPTAARR route through the plan — step 2 wave 2

The second wave of promotion step 2, the SCENUMS pattern applied to the
small Option families: each collector's `body_unsafe_for` escape conjunct
becomes the plan's `free_eligible_of` verdict under `FERN_SELFHOST_RC_PLAN`,
with the family knowledge kept — kind annotation, fresh-init proof,
not-reassigned, OPTAARR's element-alias and payload gates (a bound
`var o = xs[i]` takes no counted retain here; native's Index-shape dup is
not ported, so the name-level plan cannot license element reads).

One invariant became explicit that was implicit before: `body_unsafe_for`
refuses EVERY bare mention, so the "unmatched" families' match-refusal was
free. The plan does not model the consuming-match release channel, so the
routed gate carries `name_is_match_scrutinee` (recursive, any nesting) as
its own conjunct — granting a matched local would put a second claim on the
payload the match machinery may free.

OPTARRARR stays credit-gated: its collector IS a consuming-match window
analysis (match-index ordering, arms-use-name, used-after,
escapes-outside-stmt), not a separable escape conjunct — it moves when the
plan learns that channel.

## Measured (x86-64, census + underflow; exits match native on all four)

| probe | native | selfhost |
|---|---|---|
| opt_arr call arg, matched in callee | 200/200 clean | 200/200 clean |
| opt_str call arg | 100/100 clean | 300/0 safe leak |
| opt_arr nested match (conjunct holds) | 200/200 clean | 200/200 clean |
| opt_aarr call arg | 300/300 clean | 400/400 clean |

Pinned as the three `opt_*__callarg__read` matrix cells (the nested-match
shape duplicates generated coverage). The opt_str floor: the plan grants
but the family's fresh-init registry path refuses the `Some(mk(..))` call
form's sweep elsewhere — a bounded leak, recorded, not a hazard.

## CI-caught: the plan is not yet >= the credit gate on call provenance

Shard 7 failed two want-exact-balance suites in the LEAK direction:
`optarr_from_call_no_match` and `unmatched_err_string_is_released` — a
`var v: Result[i32[], string] = mk(r)` local the credit gate granted (no
bare mention anywhere) turned plan-refused: `rc_fe_rhs_tainted`'s user-call
arm taints on ANY tainted argument, every non-own param is taint-seeded
regardless of type, so the scalar `r` tainted `v` through its own init.
Native unt aints this exact shape through `returnsNoParamEscape` (every
return built from scalars and fresh constructions, a call-graph fixpoint
with `paramCountedRetain` as its sibling) — an oracle the rc_fe port does
not carry yet.

The staged fix is the UNION of the two individually-sound grants: the
credit-side escape proof keeps every pre-routing grant, the plan adds the
call-arg grants it alone can see (still refusing matched locals). The
credit half of the union retires when the returnsNoParamEscape port lands
— that port is the named next burn-down, and it converges rhs-taint
divergences well beyond these two suites (the self-compile RSS driver's
"real leak" class rides the same oracle natively).
