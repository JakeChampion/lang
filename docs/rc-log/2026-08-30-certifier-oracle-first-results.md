# The certifier's oracle, and what it says (2026-08-30)

`2026-08-30-certifier-transfer-taxonomy.md` ended by asking for an
oracle: the static per-path walk reported 63,070 leaks over the
self-host and five refinements each failed, which is the
aggregate-iteration trap #7803 records. The conformance leak census
(#7831) is that oracle.

## The comparison

A fixture the census records as **0 unpaired allocations** leaks nothing
on the path it takes, so every function the static walk flags there is a
**false positive with a name** — which a total of 63,070 never had.

Over the 318 clean fixtures, 91,736 functions:

**18,638 falsely flagged — 20.3%.**

And the breakdown names them. The top entries are the whole
`__method_i64_checked_*` family, each flagged once per fixture that
imports it, and drilling into one says which values:

```
values flagged in __method_i64_checked_abs: alloc x212, enum_sentinel x106
```

`checked_abs` returns `Option[i64]`, building `Some(…)` in two branches.
Both allocations are flagged at both returns.

## Two more refinements, two more negative results

That reading suggests two fixes, and neither works:

| refinement | falsely flagged |
| --- | --- |
| union at merges (baseline) | 18,638 |
| returned set widened backwards through phis | 18,638 |
| defining block must DOMINATE the return | 18,487 |

Widening the returned set through phis — on the theory that `Some(n)`
built in two branches is returned via a phi whose id differs from the
alloc's — changes **nothing**. A dominance filter, on the theory that a
value allocated on a path not taken is a merge artifact, removes
**151**, 0.8%.

## Seven refinements, seven negative results

With the five in the transfer-taxonomy note, that is seven attempts to
improve this walk by adding a rule, none of which helped materially and
two of which made it worse. The conclusion is no longer about any
individual rule:

**The walk is wrong in many small ways rather than one large one, and
filtering does not converge on it.** It wants rebuilding against the
oracle from a correct unit-holder set, one named class at a time — which
is what the oracle now makes possible and what none of the seven
attempts did.

`enum_sentinel x106` is a fair sample of the small ways: a sentinel is
not a heap value, and an `rc_inc` against one is null-guarded at
runtime, but the walk treats the retain as creating a unit.

## What is now in place

- **Ground truth**, per fixture and per allocation site (#7831).
- **A localiser**: the false positives have function names and, one
  level down, the op kinds that produced the flagged values.
- **A join budget** (#7828): the values whose ownership differs across a
  join are p99 = 10, so a path-sensitive summary is affordable when the
  walk is worth making path-sensitive.

What is not in place is a walk worth gating. That is the next slice, and
it is a rewrite rather than a refinement.
