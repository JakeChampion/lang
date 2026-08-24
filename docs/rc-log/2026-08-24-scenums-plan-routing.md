# SCENUMS routes through the plan — promotion step 2 begins

The first release family whose escape gate is the plan's verdict rather than
the credit table's: `collect_fresh_scenum_names` consults
`free_eligible_of` (computed once per function at `lower_func` entry, via
the #7477 ret-type registry) instead of `body_unsafe_for`.
`FERN_SELFHOST_RC_PLAN=0` reverts to the credit gate — a migration aid
judged by the leak matrix and the underflow trap, never a byte-identity
contract.

## Why SCENUMS first

The step-2 recon's two rankings intersected here: smallest total surface
(25-line collector, 7-line sweep arm, ONE predicate serving rebind and
sweep, shallow dec, no alias retains, no reuse or precise-drop
involvement) AND pure plan-agreement on all 10 corpus cells. The families
where release-first promotion is a census-invisible double free (str /
tuple / opt alias interplay; the elemret UAF boundary) are recorded as
later waves behind their retain-side ports.

## What routed and what stayed

Only the ESCAPE conjunct routed. The other three stay family knowledge the
name-level plan cannot supply: `fresh_scalar_enum_init` identifies the
kind, the rebind-freshness check keeps a non-fresh reassign out, and the
no-consuming-match check keeps this credit disjoint from
`consumed_scalar_enum_frees`' release. The credit is still stamped
site-keyed, so the shadow-sibling refusals are untouched (both
`shadow_siblings` cells hold).

## The call-arg question, measured before routing

The plan does not taint a plain call arg. That is native parity, not a
scoping cut: `computeFreeEligible`'s Call arm taints only Map keys/values,
uncounted variant payloads, and (single-word ABI) string args a callee
retains uncounted — a callee that KEEPS any other arg retains it through
its own counted construction stores, so the caller's sweep is balanced.
Probed before routing (x86-64, census + underflow):

| shape | native | selfhost before | selfhost after |
|---|---|---|---|
| reading callee | 100/100 clean | 100/0 leak | 100/100 clean |
| stored in returned struct | 200/0 leak | 200/100 leak | 200/101 leak |
| stored in returned array | 200/200 clean | 200/100 leak | 200/200 clean |

Two cells flip to exactly native's numbers; the storing-struct shape stays
a leak on BOTH sides by design (leak-at-worst, never a dangle — native
leaks more than the self-host there). Exits match native everywhere, zero
underflow. Pinned as the three `enum_scalar__callarg__*` matrix cells.

## Next

The remaining conjuncts and the sweep arm leave with later steps; the next
families in the sanctioned order are the small Option deep-free set
(OPTAARR / OPTSTR / OPTARRARR), then struct (fixes the two alias_param
rows with its retain moving in the same step), with str / tuple last
behind the co-extensive retain gate and dup-at-extract.

## CI-caught: the append store was uncounted

Shard 14 failed `rebound_value_escapes_to_container` with a wrong answer —
the #6127 hazard re-armed: the plan's counted-sink forgiveness assumes the
STORE retains (native's `emitArrayPush` incs any rc-typed element), but the
self-host's append retain covered only array slots and enum-field aliases.
With the credit now granted, the loop-rebind release freed a box the array
still read. The fix is the #7253 rule applied literally: the append arm
retains a source slot holding the `SCENUMS:` credit — gated on the CREDIT,
never the type, so retain and release stay co-extensive in both switch
positions (plan off ⇒ no credit ⇒ no retain ⇒ exactly the old behavior).
The sibling counted sinks were probed with the same rebind-in-loop shape:
struct-lit, array-lit, tuple-lit, and variant-payload stores all answer
correctly (two clean, two bounded leaks) — append was the one uncounted
store.
