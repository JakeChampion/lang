# 2026-09-20 — a phi of views owns nothing

A `str` local assigned in more than one place becomes a phi of views. The unit
planner marked such a phi owned, its edges were asked to supply that unit, and
`view_retain_error` refused the plan: a retain on a view's immortal box is a
no-op while the release that balances it frees the box (#9802). The refusal was
right and the ownership was not. This entry moves the decision.

## What was wrong

`phi_ownership` reads a phi's OPERANDS: a merge of borrowed parameters holds
nothing, anything else owns a unit. A view fails that test whatever it merges,
because a view arrives through `str_as` or `slice`, neither of which is a
borrowed parameter. So `var spec: str = ""` followed by
`spec = slice_unchecked(fmt, i, j)` refused, and `std/format` with it.

Making the phi unowned on its own is not safe, and this is the part that made
the change bigger than one line: `ssasem.borrow_parents` gives each value a
SINGLE parent and gives a phi none, so an unowned view phi would be anchored to
nothing and no lifetime guard would hold its sources past its last read.

## The fix

A view phi takes the UNION of its operands' anchor sets — never an operand
itself, which is defined in one predecessor and does not dominate the reads
after the merge, so naming it fails availability where naming its source
succeeds. Copying only anchor sets also keeps every phi out of every dependency
row, which is what lets a loop-carried view read as anchored to what it entered
the loop with rather than as a cycle.

Two rows then need reclosing, since `ssadeps.verify` demands the full
transitive closure: the phi's own set, and the chain of a window taken FROM a
phi, which named the phi before the phi had any anchor.

With the anchors in place the ownership rule is the simple one: a phi whose
result is a view owns nothing, whatever its operands are.

## Measured

x86-64, `FERN_SANITIZE=1` + `FERN_LEAKCHECK=1`, native x86-64 as the oracle.
Every row answers identically on the typed leg, the AST leg and native.

| shape | before | after | typed held | AST held |
|---|---|---|---|---|
| a `str` merged across a branch and around a loop | 0 of 58 | 58 of 58 | 0 B | — |
| `examples/tests/format_test` | 0 of 238 | 238 of 238 | 0 B | 10,864 B |
| `conformance/cases/format_specs` | 0 of 136 | 136 of 136 | 0 B | — |
| `conformance/cases/format_placeholders` | 0 of 130 | 130 of 130 | 0 B | — |
| `view-loop-rebinds-a-live-view` | refused | 1 of 1 | 0 B | 96 B |

That last row is the shape #9802 was opened for: a view local rebound in a loop
from a view that stays live. Produced with the phi owning a unit, it read the
live view after the loop had freed it. It produces correctly now, because
nothing retains or releases the merge and the source's own owner outlives it.

Corpus census (865 seeds), both legs run here with the same script:

| | before | after |
|---|---|---|
| programs produced whole | 819 | 822 |
| declarations produced | 83,189 of 87,203 | 83,709 of 87,219 |
| programs reporting `a view is lent, never retained` | 5 | 1 |

The denominator grows by the 16 declarations this change adds to the compiler.

## What is still refused

- **`Option[str]`** (`conformance/cases/string_slice_option`) is the one
  remaining `a view is lent, never retained`. A view in a VARIANT payload is
  not a phi, and `variant_up` asks for the unit the payload column would hold.
- **`examples/cli/fold`** now reaches `dependency unavailable at use` instead.
  Its `rest` is rebound from `rest.drop(brk)`, whose result is defined inside
  the loop, so the anchor the merge needs does not dominate the loop header.
  A loop-carried view of a source born in the same loop has no dominating
  anchor to name, which is the honest answer rather than a refusal spelling.

## Traps

- **The guard that refuses is not the bug.** `view_retain_error` was correct at
  every point; what was wrong was upstream, in who was asked to supply a unit.
  A refusal is a symptom whose cause is usually a decision made earlier.
- **An anchor is only useful where it dominates.** The first version of this
  anchored a phi to its operands, which reads naturally and fails on every
  merge: the operand lives in one predecessor. Anchor sets compose; operands
  do not.
- **A closure invariant outlives the pass that established it.** Chains walked
  from single parents are closed for free, so nothing reclosed them. Adding one
  non-chain row broke that silently for every value that had named it, and the
  failure surfaced three passes later as `incomplete dependency closure`.
