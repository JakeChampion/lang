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

So the accounting is dominated by **one unmodelled transfer form**, by
an order of magnitude. A value handed to a callee that takes it leaves
the caller's books, and until the walk knows which call positions
consume, every such hand-off reads as a leak.

(The over-release figure comes from the same unsound walk and is not
evidence of anything on its own — it is reported only so the two
directions are not confused.)

## What this changes

#7786 says the ownership signature table "is most of the work in the
certifier". That is now measured rather than estimated, and it is more
pointed than it sounds: the join representation — the part Roc's
docstring warns about, and the part that looked like the risk — is
**not** the bottleneck. The transfer taxonomy is, and within it,
call-argument transfer alone accounts for 84% of the error.

The order of work follows:

1. **Call-argument transfer** — which positions consume. `SolveOwnership`
   already answers this for defined callees and `rcsigs.go` for runtime
   helpers; the walk has to consult them, and the 0.9% opaque-call
   residue measured for #7792 is the part that cannot be answered.
2. **Store transfer** — a unit written into a container or a struct
   field. 8.8%.
3. **The remaining 7%**, which is the only part not yet characterised.
4. **Then** the join summary, on the differing set.

Building the lattice first would have been building the cheap part
first.
