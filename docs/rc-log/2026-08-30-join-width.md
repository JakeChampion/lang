# How wide is a join? (2026-08-30)

#7786 records that Roc's certifier hit a scaling failure — its
join-point summary lattice grows like the **Bell number** of the
refcounted locals a loop carries, B(12) = 4.2 million (their issue
9658) — and says any implementation here should plan the summarisation
up front rather than discover the same wall.

So: measure the wall first. Over the self-host compiler, at every block
with more than one predecessor.

## The numbers

**36,052 joins.**

| | p50 | p90 | p99 | max | over 12 |
| --- | --- | --- | --- | --- | --- |
| live pointer values | 2 | 23 | 157 | 1879 | 16.00% |
| alias classes | 2 | 23 | 154 | 1879 | 15.73% |

These supersede an earlier measurement in this log (29,108 joins, max
435, 12.9% over 12). That instrument predates the lift modelling
two-word values and call shapes, so it was counting a different — and
wrong — program.

## Two conclusions, one of them a refutation

**Collapsing by alias class buys nothing.** The hypothesis was that
because SSA represents aliasing explicitly — phis, rc-helper
pass-through — the partition Roc's lattice has to carry would be
derivable from the SSA graph instead, and the effective width would
drop. It does not: 16.00% of joins are wider than 12 counted in values,
and 15.73% counted in alias classes. Distinct live values at a join are
almost always distinct objects already.

**No lattice exponential in the width is viable here — Bell numbers are
not the binding constraint, the width is.** At p99 = 157 and a maximum
of 1879, `2^n` is as hopeless as `B(n)`. Roc's B(12) wall is *mild*
compared with what these joins would demand. A correlated summary is
not an option to be optimised; it is off the table.

## The tension that is left

The cheap alternative is a **per-value independent** summary: a
three-valued state (owned on all paths / on some / on none) per live
value, O(width) storage and no correlation tracking at all.

That is affordable, and it is also weaker in exactly the place the
certifier is meant to be strong. Independence cannot express *either x
or y holds this unit, not both* — and distinguishing a conditional
transfer from a conditional leak is the whole reason #7786 wants
per-path accounting. `emitMapCowRetainTest` is the real, correct
conditional retain that killed the original slice 1.

So the honest state is a constraint, not a design:

- correlated summaries are **sound but infeasible** at these widths;
- independent summaries are **affordable but too weak** for the check
  that motivates the work.

The next question is whether the width that *matters* is the live
width at all. A join only needs agreement on values whose ownership
state actually DIFFERS between predecessors, which is plausibly tiny
even where 1879 values are live. Measuring that needs an ownership
state per path — the certifier itself — so it cannot be answered by a
probe from outside, and it is where the design work should go rather
than into summarising a width that may be the wrong number.
