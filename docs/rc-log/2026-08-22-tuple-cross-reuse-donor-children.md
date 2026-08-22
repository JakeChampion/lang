# The cross-tuple reuse donor's children, given back

#7275. `emit_cross_tuple_reuse` recycles a dead donor tuple's box for a later
tuple construction. Whatever that box **owned** died with the recycling and
nothing gave it back.

The fourth site on the `"TUP:"` release enumeration #7271's entry opened — and
auditing it turned up a **fifth**, which that entry had flagged as unchecked.
Three limbs in all; the issue named one.

| shape (100 rounds, x86-64) | native | before | after |
| --- | --- | --- | --- |
| donor and recipient each retain a bare ident | `live=0` | `300/200` **4000** | `300/300` **0** |
| recipient takes a fresh array literal | `live=0` | `300/100` **8000** | `300/300` **0** |
| donor `(xs, ys)` — two retained positions | `live=0` | `500/300` **8000** | `500/500` **0** |
| a CHAIN: `t` → `u` → `v` | `live=0` | `400/100` **12000** | `400/400` **0** |
| the donor's element is a borrowed PARAM | `live=0` | `201/200` **40**, flat | `201/201` **0** |
| a LOOP-CARRIED recipient, fresh literal | `live=0` | `300/102` **7920** | `300/300` **0** |

`FERN_SELFHOST_NO_REUSE=1` takes every row to 0 without touching the compiler.
That switch is the attribution and it is worth reaching for first: it separates
"the reuse layer strands this" from "a credit gate denies it" in one run.

## Why the one-liner used at the other three sites is wrong here

The reuse is runtime-guarded on `__fern_rc_is_unique(d)`, and the two arms owe
**opposite** things:

- **reuse arm** (unique): the box is recycled, its children's references die
  with it, and they must be released.
- **fresh arm** (shared): the donor's box survives under its other owners — the
  dec of `d`'s own reference there never frees it — so releasing its children
  would free buffers those owners still read.

So the release sits **inside** the uniqueness test (`emit_tup_donor_releases`),
which is what separates this site from the sweep / rebind-store / precise-drop
three, where an unconditional release is correct.

**The fresh arm is not reachable from a source-level shape.**
`cross_tuple_construction_donor` admits only a non-escaping, never-reassigned
local with no mention after the construction, so the donor's box is provably
sole-owned and the runtime guard exists to make an *analysis* miss degrade
rather than corrupt. `__fern_rc_is_unique(0)` returns 0, so a null donor takes
the fresh arm too. No probe below witnesses the fresh arm, and none can: the
placement is defence for a bug that does not exist yet. Witnessing it needs a
unit on the emitted branch, not a program — which is also why "I could not
measure a difference between the two placements" is not evidence they are
equivalent.

## The second limb: the recipient's own slot tags

The issue named one leak; the array-literal row is a different one at the same
site, and the donor release does not touch it.

`emit_cross_tuple_reuse` tagged `c`'s slot from the element **expressions**, and
`elem_type_tag` deliberately coarsens a scalar-array element to the bare `"i32"`
(*"they need no element typing and broadening them would disturb the shared
destructure / kind-string consumers"*). So `(i32, i32[])` was recorded as
`"i32,i32"`, and `emit_rctuple_deep_free` — which frees by TYPE, via
`emit_tuple_type_child_drops` — found no array child. The recipient's own fresh
array literal stranded at its buffer's size: 88 B/round for an 8-element array
against 48 for a 3-element one, which is how it was told apart from the donor's.

The fix is not a new rule. `bind_var_slot`'s `ExprTuple` arm already prefers the
declared tuple type over the inferred tags for exactly this reason, and
`tuple_type_elem_tag` returns `"i32[]"` where `elem_type_tag` returns `"i32"`.
The reuse path now does the same, so the two paths agree; the write loop below
it recomputes its WIDTH tag from the element expression and is unaffected.

**Parity with the non-reuse path is the whole argument for safety here.** This
is an added dec, and "it only adds decs" proves nothing (#7271). What licenses
it is that the same slot, bound by the same statement without a donor in scope,
already gets this release under the same `"TUPRCS:"` credit — the credit was
granted and the release was lost to a coarsened tag.

## The chain, and why the donor release consults two registries

`u` recycles `t`'s box and then donates it to `v`. `u` was bound by
`emit_cross_tuple_reuse`, so `bind_var_slot` never saw it and it has no
`TUPELEM:` entry — yet its fresh array literal is owned, by the `"TUPRCS:"` deep
free its donor status then suppresses. A donor release keyed only on the
recorded element kinds strands it (12000, measured).

`emit_tup_donor_releases` therefore replays whichever of the two classes the
donor slot is in:

- `"TUP:"` → the recorded per-position retains (`tup_elem_kinds_of`).
- `"TUPRCS:"` → `emit_tuple_type_child_drops`, i.e. `emit_rctuple_deep_free`
  minus its box dec — the box is recycled here, not freed.

`reclaimable_names_of` collects the two from disjoint name sets, so a slot is in
at most one and the two cannot both fire. That exclusivity is load-bearing, and
it is the same one #7226's trap section records: a slot in both classes gets its
**box** dec'd twice.

## The fifth site: the loop-carried recipient's prior box

#7271's entry closed by naming `emit_reuse_recip_prior_release` — the release of
a loop-carried recipient's PREVIOUS-iteration box — as the one site on the list
still unaudited, and predicted it strands one buffer per iteration. It does, and
worse than predicted: **7920 bytes over 100 iterations, both the buffer and the
box**, because the site was not reached at all.

Two separate faults, and the first hides the second:

- Its call is gated on `slot_is_reclaimable_tuple` — the `"TUP:"` class only —
  so a `"TUPRCS:"` recipient never reached it and its prior box was never even
  dec'd. The gate now admits both classes.
- What it emits is a bare cow-guarded `__fern_rc_dec`, which is the whole
  release only when the box owns nothing.

Both fixes are the same shape as the donor's, so `emit_tup_box_child_releases`
serves both callers. It emits nothing for a slot in neither tuple class, which
is what keeps the struct recipient (`emit_cross_struct_reuse`, the function's
other caller) byte-identical.

Its null guard is not optional here and is the difference from the donor call:
the donor is inside `__fern_rc_is_unique`, which returns 0 for null, but a
loop-carried recipient slot holds its entry zero on the first pass and
`emit_tuple_type_child_drops` dereferences the box to reach each position.

**The lesson the fifth site adds to the fourth.** #7271's entry says to
enumerate the sites by grepping `tup_elem_kinds_of` against every
`slot_is_reclaimable_tuple` release. That grep finds this site — and finding it
is not the same as auditing it, because the fault was in its **gate**, one line
above the release. A site that a class never reaches looks correct at the
release, and reads as "no leak here" from the code alone.

## Non-vacuity

All nine cases in `internal/e2eselfhost/self_host_tuple_cross_reuse_elem_test.go`
fail with the fix reverted and the compiler rebuilt — the leak signature, not an
answer change. The alloc counts are identical either way, which is what says the
pairing was already firing and the release was simply missing.

Each case asserts the exact `allocs` count as well. It sits one box per round
below the `FERN_SELFHOST_NO_REUSE=1` count, so a future change that stops
pairing these shapes fails there instead of passing with nothing left to
release.

## Three leaks found alongside, none of them this path

Each with `__rc_underflow()` reading 0 — leaks, not over-releases. The two
self-host ones reproduce identically under `FERN_SELFHOST_NO_REUSE=1`, so
neither is the reuse layer's.

- **A mixed rc tuple is never released at all** — #7281. `var t: (i32[], i32[])
  = (xs, [i + 2, i + 3])` earns `"TUPRC:"` (the rebind path) but not
  `"TUPRCS:"`, because `tuple_arg_payload_fresh` requires *every* rc position to
  be a fresh literal, and position 0 is a live local. So a single-bind tuple of
  that shape has no sweep release at all, box included: `allocs=300 frees=0`,
  12000 bytes over 100 rounds. Make either position rc-free and it balances;
  only the mix falls between the two classes. The all-or-nothing gate is the
  cause — the class needs the per-position kinds list `"TUP:"` already has.
- **A tuple bound from another tuple local releases nothing** — #7282. `var v:
  (i32, i32[]) = t;` — `allocs=200 frees=0`, 8000 over 100 rounds. The alias
  escape-flags `t`, so this is a credit denial rather than a missing release
  site, and `v`'s own bind is in neither collection.
- **Native has this exact bug** — #7283. Same donor, same stranded element
  retain: `300/200`, 32 B/round, against the self-host's `300/300` after this
  change. `internal/ir` has no reuse switch, so the attribution there is the
  alloc count — deleting the statement that makes the donor dead early takes it
  to `400/400` and clean, and that 400 → 300 is one recycled box per round.
