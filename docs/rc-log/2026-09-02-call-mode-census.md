# The call-mode census: #7792's population, with the axis that decides it

#7792 asks whether Fern should emit ownership-mode-specialised copies of
a callee, Roc-style, for call sites where the caller owns a dying value
and the shared signature borrows the position. It was filed as "measure
first", and the tracker carried three answers — 662, 3,600 and 3,599
sites — none committed, the third correcting the second. None of them
separated the sites where a variant would delete a retain/release PAIR
from the sites where it would only move the caller's one release into
the callee. That axis is what decides the issue, and this entry is the
reproducible derivation of it.

`ssa.CallModeSites` classes every pointer argument at every direct call
with a solved signature by four facts: the callee's solved mode, how the
caller holds the argument (`UnitsOf`), whether the value dies at the
call, and whether the CALLEE retains that parameter (`ParamRetained`).
An owned variant pays only when the callee retains — a callee that only
reads its parameter would release it at exit instead of the caller
releasing it after the call, and nothing is saved. The opposite
direction, a borrowed variant of a consuming callee, is counted only
where the caller's pre-call retain is witnessed in the op stream:
liveness over phis calls a loop-threaded value live at every call in
the loop, and only the retain says the call cost anything.

`TestX86_64CallModeCensus` runs it over the conformance corpus lowered
post-battery (rule 14); `TestOwnershipSolverAgreesWithTheLoweringsOwnVerdict`
logs the same histogram over the self-host compiler, which is the
workload #7792 names. Generated drop thunks are not callees here:
consuming is what they are for.

## The answer

Self-host compiler, x86-64, post-battery, 6,469 functions, 0 lift
failures:

    74,188 pointer arguments at solved call sites, 65,314 carrying a unit

    optimal                          63,972
    borrowed-variant:pair             8,099   12.4% of unit-carrying sites
    owned-variant:deferred-release    1,461
    owned-variant:pair                  656    1.0%

Conformance corpus, 463 fixtures: 4,400 sites, 3,037 carrying a unit;
34 owned-variant pairs over 12 callees, 148 borrowed-variant pairs.

## The population the issue was filed on is a signature story

The 3,600 the tracker carried was 76% two callees. Both have moved, and
neither by a variant:

- `LowerState.emit(op)` — 2,204 sites — now solves `[consumed consumed]`.
  Owned-by-default reached `ir.Op` when the gate became "is the deep
  drop wired" (#8056), so a fresh op is a transfer and the site is
  optimal. The head of the old population was the signature being
  wrong for the callee, and the signature fixed it.
- `EmitState.write(text)` — 408 sites — is `[borrowed borrowed]` with
  `retained=[true false]`: `strbuf_append` copies the text, the callee
  never retains it, and an owned variant would move the caller's one
  release and delete nothing. It was never a pair. It is the whole head
  of the deferred-release class.

What is left in the owned direction is 656 sites over 218 callees with
the largest at 90 (`x86_gas_bad`, a fresh error string into a stored
`string` parameter — strings are not owned-by-default). A long thin
tail is exactly the case the issue's own code-size objection is
strongest against, and the number is stable across the pre-battery lift
(714) and the battery (656).

## The larger population runs the other way, and is not a variant either

8,099 witnessed pairs — a retain the caller performs for the call, then
the callee's exit sweep releasing the parameter:

    irlower__lower_expr            535     Par_peek_punct    338
    irlower__expr_struct_type      217     Par_peek_ident    121
    checker__check_expr            207     LowerState_emit   117

Traced on `Par.peek_punct`: `v4 = __fern_rc_inc(v1); v5 = peek_punct(v4)`
at every caller, `ParamConsumed=[true]` and `__drop_struct_parser__Par`
on every exit of the callee. A read-only peek pays an inc and an
`is_unique`-gated drop per call. The receiver is owned because
`paramEscapesInFn` counts a returned PROJECTION of `p` (`peek` returns
`p.toks[p.pos]`) as `p` escaping, so borrow inference never reaches
`paramVerdictBorrowed` for it — and the return-transfer inc that
projection already carries is what makes borrowing it safe.

Per callee: 697 callees where EVERY caller retains (2,957 sites —
`Par_peek_ident` 121/0, `Par_peek_punct` 338/5, `check_expr` 207/7),
462 where callers split (5,142 sites — `LowerState.emit` 117/4,383,
`Par_advance` 93/381). The first group is a signature that no caller
wants; the second is the honest cost of a signature most callers do.
Neither is a variant question: a callee every caller borrows from
should BE borrowed, which is borrow inference's precision, not a second
copy. Filed as its own issue from this entry.

## Traps

- 140,668 of the first run's "sites" were exit-sweep calls to generated
  drop thunks, which are in the solved set because they have bodies.
  Excluding them took the self-host total from 214,856 to 74,188 — the
  74,299 the tracker's second measurement reported over the same
  program.
- The argument at a call is usually a retain's RESULT. `unitCarriersOf`
  walks forward from a value, so starting the liveness walk at the
  argument instead of `Units.Root(arg)` misses every use of the object's
  other names and reports the caller's live value as dying.
- A later `__fern_rc_dec` is not a use, but `__method_Array_push` and
  `__method_Map_set` carry the same release effect and ARE uses: the
  unit goes to a container the caller goes on to hold. The runtime's
  releases return their operand and the builtins do not, which is what
  `wantedUses` keys on.
