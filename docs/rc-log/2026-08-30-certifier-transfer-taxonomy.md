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

That is the work, and it comes before any of the transfer forms:

1. **Establish what holds a unit.** A pointer-valued temporary is
   usually a borrow. Until the walk starts from a correct set of
   unit-holders, every transfer form is being fitted to noise.
2. Then the transfer forms — store (8.8% of the values by destination),
   and whatever survives.
3. **Then** the join summary, on the differing set (#7828).

The probe cost about half an hour and refuted two orderings, including
the one this note originally recommended. That is what it was for.
