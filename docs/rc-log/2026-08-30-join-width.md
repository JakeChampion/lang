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
| **differing across predecessors** | **0** | **1** | **10** | 1878 | **0.75%** |

61.11% of joins have **no** value their predecessors disagree about.

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

## The live width is the wrong number

A join summary does not have to relate every live value. It has to
relate the ones its predecessors **disagree** about — a value both arms
still hold, or both have discharged, needs no summary at all. That set
is tiny: p50 = 0, p99 = 10, and 61% of joins are empty.

So the tension below dissolves. A **correlated** summary over the
differing set is affordable — B(10) = 115,975, against B(157) which has
no name — while the same summary over the live width is not. The
certifier should carry correlation only across the disagreement, and
the 0.75% of joins wider than 12 need a documented fallback rather than
a redesign.

The measurement's ownership walk is deliberately coarse: a two-state
holds/discharged lattice, direct releases only, and a join rule that
keeps the stronger claim. It is an estimate of the ORDER, not a
certified figure — but 0 against 157 at the median, and 10 against 157
at p99, is not a margin that turns on the approximation.

## The tension that was there before that measurement

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

That was resolved by measuring the differing width above, which turned
out to be answerable by a probe after all: a coarse forward ownership
walk is enough to size the disagreement even though it is nowhere near
enough to certify it.

What is left for the certifier proper is the fallback for the 0.75%
tail — `wasm_ir__extern_wrappers` reaches 1878 differing values — and
the accounting itself.
