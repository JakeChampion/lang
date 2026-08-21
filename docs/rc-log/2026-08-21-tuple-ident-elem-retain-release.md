# The bare-ident tuple element's retain, given back

#7226. Tuple construction retains a bare-ident element naming an rc-container
local; nothing ever released it.

| shape (200 rounds, x86-64) | native | self-host before | after |
| --- | --- | --- | --- |
| `var xs = [i,i+1]; var t: (i32, i32[]) = (i, xs)` | `live=0` | `allocs=400 frees=200` **8000** | `400/400` **0** |
| `(xs, ys)` — two idents | `live=0` | `600/200` **16000** | `600/600` **0** |
| ident read after the tuple | `live=0` | **8000** | **0** |
| the ident is a borrowed PARAM | `live=0` | **40**, flat | **0** |

## Why the counts did not balance

`arr_box` rc=1 → construction `__fern_rc_inc` (rc 2) → exit sweep's `is_arr`
dec (rc 1). incs 1, decs 1, start 1, final 1. **Exactly one release was owed and
nobody owed it.** The tuple box itself was freed all along by the `TUP:` credit;
the 40 B is the element buffer.

The param row is the same arithmetic with the other dec missing: the `is_arr`
sweep starts at `n_params`, but `slot_is_rc_container` has no such guard, so a
param element was retained and never dec'd at all. It reads as bounded rather
than per-round only because one buffer is retained repeatedly.

## Why the release is keyed on the retain, not on the type

`emit_tuple_type_child_drops` already exists and frees by declared element type.
Reusing it here is the use-after-free `TestSelfHostRcTupleSweepHazardsX86_64`
pins: a type-driven walk dec's every rc-typed position blind, including one the
construction never retained.

So `bind_var_slot` records the retained positions — from the **same**
`slot_is_rc_container` test that decides the retain, on the same slots — under a
`TUPELEM:<name>|<kinds>` registry entry, mirroring `add_clo_cap_kinds`. The
scope-exit sweep and the rebind store both replay exactly that list. A position
the construction declined is never in it, so the two cannot drift.

Only the ARRAY limb is recorded. A string element's source local is
escape-flagged by `expr_unsafe_for`, so it earns no `STR:` credit and its own box
is never swept; releasing the tuple's reference alone would balance the tuple and
still strand the local. That limb is a **credit**-side gap, not a release-side
one — see below.

## The rebind half is not optional

The first attempt released only at scope exit, and moved 8000 → 7960: one buffer
of two hundred. A loop-body `var t = (i, xs)` re-binds ONE slot every iteration,
so the scope-exit sweep sees only the last tuple; the other 199 retains are given
back by the store that supersedes each box. That is `emit_tup_elem_reclaim_store`,
the tuple sibling of `emit_str_reclaim_store` — cow-guarded like it, and with the
element walk under its own null guard.

**n-1 of n freed is the signature of a scope-exit-only release.** It looks like a
0.5% improvement and is actually a missing mechanism.

## Two guards, and why the box's own guard is not enough

`__fern_rc_dec` null-guards, so the existing box dec tolerates a zeroed slot —
an untaken branch, or the first pass of a loop rebind. The `op_tuple_get` that
reaches an element does **not**: it dereferences. The element walk therefore
carries its own `slot != 0` guard, and `untaken_branch_null` in the new suite is
the case that fails without one — as a fault, not a leak.

## Witnessed at fault level

Reverting the change and rebuilding: all five cases fail with the leak signature
(`allocs=200 frees=100 live_bytes=4000` on the headline shape) and pass with it.
`internal/e2eselfhost/self_host_tuple_ident_elem_retain_test.go`, three legs.

## What is still open on #7226

- **The string limb**, and it is the larger leak: **80 B/round, unbounded**
  (16000 at 200 rounds, 32000 at 400 — exactly 2.0× per doubling), where the
  array limb was 40. The issue's table calls this row clean; that row used
  `var s = "ab" + "c"`, which constant-folds to an immortal literal
  (`constfold.fern:209`, box rc=-1), so it measures a constant. Re-measure with
  `w("ab")` before believing any string-element number.
- **The same ident in two tuples** — `(i, xs)` and `(i+1, xs)` in one frame —
  still strands: measured `allocs=600 frees=200`, where two tuples over
  *different* idents is flat. A credit denial upstream of the release, not a
  missing release.
- **The assign-form rebind** `t = (k, ys)` goes through `lower_stmt_assign`'s
  `emit_arr_store`, which this change does not mirror; each assign's retain
  strands. The `var` form is covered.

None of the three is a regression: this change only ever adds decs, and the
underflow counter reads 0 on every probe above.

## Trap

The recorded negative result on #7226 (widening `tuple_arg_payload_fresh` +
`tuple_lit_has_rc_child` → exit 107) is real but its cause is class overlap, not
"the element side cannot carry the release". `tuple_lit_is_fresh_scalar` accepts a
bare ident **purely syntactically**, so the widened slot held `TUP:` *and*
`TUPRC:` at once and the **tuple box** was dec'd twice — not the element the fix
was aiming at. `live_bytes` 8000 → 7960 is that box, freed once. Keeping the slot
in exactly one class is what makes this change safe, and it is why neither gate is
touched here.
