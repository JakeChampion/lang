# The sole-owned guard census: the #7787 headroom is zero, reproducibly

#7787 asks to delete the runtime `is_unique` guard where the value is
provably sole-owned. The tree carried two prior figures — "14,313
guards, 0 conservatively elidable" (2026-08-30) and "7,126 guards,
headroom still 0" (2026-08-31) — and the tracker carried a third,
"352 of 2041 (17.2%) provably sole-owned", with no committed
derivation. They could not all be right, and none recorded its
denominator, so none could be checked against another.

This entry is the reproducible derivation. `ssa.SoleOwnedGuards` is a
per-site proof over the lifted form; `TestX86_64SoleOwnedGuardCensus`
runs it over the whole conformance corpus lowered POST-BATTERY (rule
14: the battery deletes half the guard population, so a pre-battery
count overstates shipped code) and logs the denominators.

## The proof

The runtime guard is four legs — non-null, above the guard page, not
a static sentinel, rc == 1 — so "sole-owned" in the SSA sense
discharges only the last, and the proof accepts nothing weaker than a
producer the result axis documents as born rc=1 with a live header
(`RcResultOwned`). Then: no CFG cycle at the guard, no phi feed, and
every use of the value's whole rename family other than the guard
itself strictly after it — so nothing between birth and guard can
retain, store, escape, or run twice. Each condition has a unit test
that fails without it.

## The answer

    454 fixtures lowered post-battery, 2160 functions, 1 lift failure
    7140 guards, 0 proven sole-owned, 0 unmapped

    origin:borrowed          3097   loads, borrowed params
    origin:none              1138   sentinels, statics
    origin:merged            1009   phis — per-path, not attempted
    fresh-not-rc1-producer    868   raw allocs, unclassified fresh
    owned-callee-producer     711   solver-proven ReturnOwned callees
    origin:unknown            293   unclassified call results
    origin:transferred         24   consumed params

Two results worth more than the zero:

- **Not one guard in the corpus roots at a table-classified rc=1
  producer.** The path conditions were never even reached from the
  proven branch; the binding refusal is the producer/origin, at 100%
  of sites.
- **The obvious widening buys nothing.** A solver-proven
  `ReturnOwned` callee proves the caller holds a unit, not that the
  count is 1 — but before that argument even has to be made, all 711
  such sites already fail the path conditions
  (`owned-callee-otherwise-proven` counted 0).

So three measurements now agree the statically-elidable population is
zero, and the census names what any future attempt has to move:
either a per-path story for the 1,009 merged origins, or a
strengthened result axis that turns raw-alloc and unknown producers
into classified ones — and then the path conditions still stand in
front of all of it. The 352/2041 figure is retired as unreproducible.

The census stays as a gate rather than a one-off: it pins provenance
totality and lift coverage, floors the population so a broken
enumeration cannot read as an empty one, and its histogram is the
scoreboard any widening argument reports against.
