# The borrowed-parameter alias leg, ported to the self-host

#9291, the self-host half of #9244. `var y = p` where p is a borrowed
parameter took the alias transfer inc on the self-host and balanced it with
y's exit dec — a wasted pair per call rather than native's leak, because the
self-host's array sweep does dec an alias slot where native's skips it.

```fern
function g(xs: i32[]): i32 {
  var v: i32[] = xs;
  return v[0] + xs[0];
}
```

Before, `-rc-plan` for `g` reported `aliasBindIncs: 2:2=v`; after, the row is
gone, which is native's answer since `8389bc577`. That pair was the last
`aliasBindIncs` divergence in `TestSelfHostRcPlanDiff`, whose
`alias-bind-param-source` anchor moves from `2:2=v` to the empty row and whose
`fe-string-param-alias` entry loses its `aliasBindIncs` divergence outright
(both sides now cancel; the freeEligible / lastUses rows still differ, for the
reason that entry gives).

## What was already there

The rc-log's 2026-09-14 entry closes by saying the self-host "has not taken
#4402's dead-alias cancellation at all". That was already stale when written:
all four limbs (`arr_`/`str_`/`struct_`/`tuple_dead_alias_bind`) exist, fed by
`dead_alias_of` → `da_scan` site keys. What was missing was only this third
leg — and one line per limb, `if (src < 0 || src < se.frame.n_params) { return
false; }`, which refused a parameter source outright.

`da_scan`'s admission proxies could not be widened in place either: they prove
"owned local" through `da_decl_array_evidence` / `da_decl_string_evidence`,
which look for the source's declaration **in the body**, and a parameter has
none.

## The change

Three parts.

- `dead_alias_borrow_params_of` names the parameters this frame never
  releases: not `own`, not in `consumed_params_of`, not shadowed by a local of
  the same name, not scalar. A reassignment is refused by `da_scan`'s existing
  `assigned` gate.
- `da_scan` gains the leg, gated on y's annotation naming a non-scalar type
  (the evidence proxies cannot help here), on `ret_scalar` when y is mentioned
  in a return, and on `da_bp_confined_body` — a whitelist walk over every
  statement kind where each mention of y must be an index base, a field-read
  object, or a `len` receiver. Anything else (a call argument, an array or
  struct literal element, a slice base, a lambda capture) is an escape, and a
  borrowed parameter's reference is the caller's.
- The four limb predicates ask `da_bp_src_slot` instead of their
  "credited local slot" test when the source is such a parameter. That test
  stands in for "the source outlives the alias's last read", which the caller
  holding p across the call establishes outright.

Recording p in `dalias.srcs` is the safety half, and it needed no new code:
`pd_filter_names` already drops the precise-drop rows naming a borrow source,
which is native's `borrowSources` refusal.

## Traps

**The module's borrow oracle is the wrong source set.** The first draft asked
`param_is_borrowable`, which returned false for every parameter this leg
exists for: `body_unsafe_for_match_borrow` reads an aliasing binding of the
parameter as a reason to refuse borrowability, so the oracle refuses p
*because* of the very alias being cancelled. Measured as `nbp=0` on `g`.

**Only the array limb has a pair to cancel today.** Probed with all four kinds
in one module: the string, struct and tuple ladder clauses gate on the SOURCE
slot being credited (`slot_is_reclaimable_str` and friends all return false
below `n_params`), so a parameter-sourced alias of those kinds never took the
retain and `aliasBindIncs` was already empty. Their relaxation is therefore
inert — kept anyway because the ladder and `bind_var_slot` consult the same
predicate, which is what stops the two halves of the pair from drifting if a
credit path for those kinds ever appears.

## Gates

`TestSelfHostRcPlanDiff` (with two new refusal fixtures:
`alias-bind-param-source-returned`, where y leaves the frame whole, and
`alias-bind-param-source-call-arg`, where a user callee is not a read-through
— both anchored on the retain being KEPT on both sides).
`TestSelfHostLeakMatrixX86_64` is the runtime gate: its `*__fnscope__alias_param`
and `*__if_block__alias_param` cells are exactly this shape across every kind,
each run 100 rounds against a source main keeps live, with
`__rc_underflow_count()` reported as exit 99. No verdict moved.
Also green unchanged: `TestSelfHostAllocCountMatrixX86_64`,
`TestSelfHostConstructionRetainMatrixX86_64`,
`TestSelfHostContainerSinkMatrixX86_64`, `TestSelfHostIRVerifyRc` and its
corpus sweep, `TestSelfHostDoubleFreeSilentWithoutSanitizeX86_64`.

## Next

The RECLAIM side is still where the port's work is. Nothing in this leg
touches it: a borrowed parameter has no release in this frame to make precise.
