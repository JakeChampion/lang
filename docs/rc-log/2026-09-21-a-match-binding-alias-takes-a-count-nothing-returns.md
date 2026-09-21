# A match-binding alias takes a count nothing returns

#9923. `var y = c` inside a match arm, where `c` is the arm's BINDING, stranded
one reference per arm execution.

```fern
match (r) {
    Some(c) => { var kept: u8[] = c; total = total + kept.len(); },
    None => { },
}
```

50 rounds, x86-64, `FERN_LEAKCHECK=1`: `allocs=200 frees=160 live_bytes=1280`
— the 40 payloads the `Some` arm ran on. arm64 and wasm identical.

## Cause

A binding is bound WITHOUT an inc: `bindingSlotScoped` hands the arm a borrow.
So it is not an owned rc local, and neither of `computeBorrowedAliases`' legs
claimed it — the dead-alias leg wants `freeEligible` on both ends, and the
borrowed-parameter leg wants a parameter. The Var lowering still emitted the
transfer inc, and the exit sweep skipped the dec because an alias is never
freeEligible.

Exactly the failure the borrowed-parameter leg (#9244) was written for, one
source short. The new leg cancels the inc on that leg's reasoning, with the
SCRUTINEE in the caller's place: it owns the payload across the whole arm, and
the only release the arm emits is the fresh-call reclaim at the join, which
runs after the body.

## The trap, again

The leg did not fire when first written, and the reason is the one #8003's
boxed half already cost a round: `needsRcIncOnAlias(v.Init, b)` reads
`exprType` on the init, and a match binding is NOT IN SCOPE while rc analysis
runs, so it answers nil and the gate refused every shape. The inc being asked
about is emitted later, from the arm, where the binding is in scope — the
predicate was being asked before its operand existed. The binding's own type
(`matchBindingTypes`) is what the analysis has, and `rcIncOnAliasType` takes it
directly.

Worth stating as a rule rather than a second anecdote: **any rc predicate that
reaches for `exprType` is unsafe to ask about a match binding from analysis
time.** Both defects in this area so far have been that.

## Two halves, measured apart

| shape | before | after |
| --- | --- | --- |
| bound scrutinee, `var` read only | 200 / 160, 1280 B | 200 / 200, 0 |
| DIRECT call scrutinee, `var` read only | 150 / 110, 1280 B | 150 / 150, 0 |
| arm `var` aliasing an outer local (control) | balanced | balanced |

The second row is the other half. `docs/rc-log/2026-09-21-a-counted-escape-is-
not-an-escape.md` had to leave a `var` init OUT of `bindingUsesExcused`,
because an uncancelled init takes a real inc and admitting it would have let
the join release a payload the var still counted. Cancelling that inc is
exactly the precondition that was missing, so the var init is now an excused
use and the #8003 reclaim reaches an arm that binds through a `var`.

The same predicate decides both: refusing to TAKE a count and giving one BACK
need the same fact, that every escape is counted. `bindingReleasableInArm`
answers both; its `!moveSites` guard is what refuses an escape whose inc would
be skipped.

## What it does not reach

An arm alias that escapes into an outer POINTER local still leaks:

```fern
Some(c) => { var kept: u8[] = c; chunk = kept; }
```

2080 B before, 800 B after. The payload half reclaims; the 50 × 16 B that
remain are `chunk`'s own empty-array headers, because `chunk` inherits the
alias's free-eligibility taint through `rhsTainted`'s Ident arm and loses its
drop. That is a different cause and NOT fixable by a rule here:
`computeFreeEligible` runs before `computeBorrowedAliases` (rc_analysis.go
:347 and :361) and the dead-alias leg reads its result, so untainting a
counted destination needs the pipeline reordered. Filed as #9948.

The taint is conservative in the safe direction — measured correct values and
sanitizer-clean on x86-64 and arm64 across every shape above, including the
payload returned out of its frame and pushed into an array that outlives the
loop — so it costs bytes, not soundness.
