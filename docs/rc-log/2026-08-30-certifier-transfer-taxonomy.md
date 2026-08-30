# The join lattice was not the hard part (2026-08-30)

`2026-08-30-join-width.md` settled the join representation: a correlated
summary is affordable if it is carried across the values a join's
predecessors **disagree** about (p99 = 10) rather than the values live
there (p99 = 157).

With that answered, the next question was whether the accounting itself
would work. It does not, and the reason is not the joins.

## The smallest accounting that produces a number

One state per pointer value — holds a unit, or discharged — walked
forward to a fixpoint, seeded from the solved parameter ownership, moved
by `ir.RcReleases` / `ir.RcRetains`, and asked two questions:

- a value still holding a unit where the function returns, and not
  returned, is a **leak**;
- a release of a value holding no unit is an **over-release**.

Over the self-host compiler, 6,370 functions modelled (97.8%):

| | reported |
| --- | --- |
| leaks | **63,070** in 3,152 functions |
| over-releases | 26,767 in 1,637 functions |

The compiler does not leak 63,070 times. This is the model being wrong,
which is what the probe was for.

## Where the noise comes from

Classifying the 63,070 by what happened to the value:

| | count | share |
| --- | --- | --- |
| **passed to a call** | **53,099** | **84.2%** |
| stored to memory | 5,539 | 8.8% |
| neither | 4,432 | 7.0% |

The obvious reading is that the accounting is dominated by one
unmodelled transfer form: a value handed to a callee that takes it
leaves the caller's books, so until the walk knows which call positions
consume, every hand-off reads as a leak.

**That reading is wrong, and measuring it is the only reason I know.**
Teaching the walk which positions consume — `SolveOwnership` for
defined callees, `rcsigs.go` for runtime helpers — moves the count from
63,070 to **62,853**. Two hundred and seventeen, 0.3%.

*Passed to a call* is not *passed to a consuming position*. Almost every
call argument is borrowed, correctly, so the 84.2% is a description of
where the values go, not of why they are miscounted.

(The over-release figure comes from the same unsound walk and is not
evidence of anything on its own — it is reported only so the two
directions are not confused.)

## What this changes

Two things are established, and one deliberately is not.

**The join representation is not the bottleneck.** The part Roc's
docstring warns about, and the part that looked like the risk, is not
what stands between here and a working certifier. #7828 settled it; this
says nothing else was waiting on it.

**Nor is call-argument transfer**, which was the obvious next suspect
and is worth 0.3%.

**What the units actually are has not been established.** A first
attempt to classify the 63,070 by where their unit came from —
allocation, retain, consumed parameter — produced a large "unknown"
bucket that turned out to be an inconsistency in the probe's own
bookkeeping rather than a fact about the program, so it is not reported
here. The honest state is that the walk marks values as unit-holding
that should not be, and which values and why is the next question.

## Five refinements, five negative results

Each measured rather than argued, over the same 6,370 functions:

| refinement | leaks | over-releases |
| --- | --- | --- |
| baseline (holds/gone boolean) | 63,070 | — |
| plus consuming call positions | 62,853 | — |
| counts instead of a boolean | 63,124 | 151,977 |
| plus call results owned | 107,257 | 143,907 |

Counts were the most promising. Perceus routinely emits inc-then-store,
after which the local holds one unit and the container holds another,
and a two-state lattice cannot represent that. It made no difference to
the leaks and exposed 151,977 **negative** counts — values the emitted
code decrements that the walk never incremented, which says the walk
does not know where units come from. Marking call results as owned, the
obvious missing source, made the leaks 70% worse.

## The pattern is the finding

Five refinements, none helping, two actively harmful, each judged by a
total. That is the failure mode `2026-08-30-lift-two-word-model.md`
records for #7803: two attempts at that work failed by iterating against
an aggregate, which cannot separate "this fix exposed an older error"
from "this fix caused one". What broke the deadlock there was a per-item
ORACLE and a per-class breakdown.

There is no oracle here yet, and building one is the next slice.

**It cannot be `rc_analysis.go` / `rc_insert.go`.** Those decide where
the incs and decs go, and the emitted incs and decs are exactly what the
walk reads — the same model twice, which is what the differential
discipline exists to avoid. An earlier revision of this note recommended
that comparison; it is wrong.

The independent source is the **runtime**. `FERN_LEAKCHECK=1` and
`__rc_underflow_count()` observe what the counts actually did. Narrower
than a static oracle, since it sees executed paths only, but independent
— the property that matters — and it localises: one function whose
dynamic verdict disagrees with the static walk is a place to look, where
a total of 63,070 is not.

So the order is:

1. **An oracle.** Dynamic unit accounting over a small corpus, compared
   against the static walk per function.
2. **Then** whatever its disagreement breakdown says, one class at a
   time — the method that worked for the stack.
3. **Then** the join summary, on the differing set (#7828).

Everything above about transfer forms is a hypothesis awaiting that
oracle, not a plan. The probes cost about an hour and refuted five
orderings, two of which this note recommended in earlier revisions.
That is what they were for.
