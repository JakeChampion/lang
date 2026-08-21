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

## The gate the first version was missing

The release as first landed over-released a tuple whose owned-pointer element is
EXTRACTED — `return t.1`, or `var u = t.1`. The extraction hands the element's
reference to a NEW owner, so a release at the tuple's scope exit is the second
claim on one reference:

| `return t.1` out of a frame, 100 rounds | native | self-host |
| --- | --- | --- |
| first version | 40 | **99** (rc underflow) |
| gated | 40 | 40, flat |

The fix is the gate the `TUPRC:` class has always applied for exactly this shape
— `rctuple_payload_escapes`, whose own comment names the failure mode: *"a whole
owned-pointer-element extraction … leaving a live alias to over-release"*. It
distinguishes a scalar element copy (`t.0`), an indexed read (`t.1[j]`) and
`.len()`, which stay borrows, from a bare pointer extraction, which does not.

The credit now requires a tuple type ANNOTATION rather than skipping the gate
when the type is unknown, which is the opposite of what `TUPRC:` does with an
un-annotated binding. Without a type there is no way to tell a scalar copy from a
pointer extraction, and the wrong guess in that direction is an over-release; an
un-annotated tuple keeps `TUP:` and simply does not earn the element release.

**The trap this set, twice.** The over-release is invisible to both the answer and
the byte count: a doubly-released block returns to the freelist, so the arithmetic
still comes out right and `live_bytes` still reads 0. The first version of the
hazard test asserted only the answer and **passed against the broken compiler**.
Only `__rc_underflow()` separates the two readings — the same lesson the sibling
`string[]` entry records from the other direction, and the reason every probe in
`self_host_tuple_ident_elem_retain_test.go` now ends with that check.

## The registry is keyed on the SLOT, not the name

`clo_cap_kinds`, the model for this registry, keys on the variable name. That is
wrong here. `tagged_value_of` returns the **first** entry matching a key, and two
same-named tuple locals in sibling blocks are two slots under one name — so a name
key hands one block's kinds to the other block's slot and releases a position that
tuple never retained. Measured as a 4000-byte strand on

```fern
{ var t: (i32, i32[]) = (i, xs); … }     // retains position 1
{ var t: (i32[], i32) = (xs, i); … }     // retains position 0
```

and it is a live-buffer release waiting to happen the moment the two positions
disagree about what is owned. Slot indices are assigned by `add_local` and never
renumbered, and unlike a name they also survive `retire_locals`' block-exit rename
— which is why the sweep previously had to reach for `reclaim_slot_name` and now
does not.

## The third release site, and why the fix looked barely effective

A `"TUP:"` box has more than two release sites, and the element release was added
to two of them. The **precise drop-on-last-use** (`irlower.fern`, the
`slot_is_reclaimable_tuple` branch of the precise-drop loop) frees the box and
then **zeroes the slot** — so the exit sweep, the one site that does replay the
element kinds, finds null under its guard and releases nothing. The two are
*alternatives, not a sequence*.

It fires whenever the tuple's last top-level mention is **not** the final
statement, which is most code:

| body, 60 rounds | before | after |
| --- | --- | --- |
| `return t.1[0];` | 0 | 0 |
| `var acc = t.1[0]; return acc;` | **2880** | 0 |
| `var acc = t.0;` — a *scalar* element read | **2880** | 0 |
| `var acc = t.1.len();` | **2880** | 0 |
| `acc = t.1[0];` — plain assignment | **2880** | 0 |

The stranded bytes track the SOURCE array's size (2 elems → 40 B/round, 3 → 48,
6 → 72), not the tuple box's — the box is freed either way; what is lost is the
construction retain.

Its comment asserted "the scalar-literal gate means no rc element to walk". That
was true when the precise drop was written and became false the moment
`slot_is_rc_container` idents began retaining at construction:
`tuple_lit_is_fresh_scalar` accepts a bare `ExprIdent` as a "scalar" element.

**The rule this establishes:** the invariant in `add_tup_elem_kinds`' own header —
*"the retain and the release it owes cannot drift apart"* — binds every site that
can free a `"TUP:"` box, not just the sweep. Enumerate them from
`grep tup_elem_kinds_of` against every `slot_is_reclaimable_tuple` release, not
from the site the bug was found in.

One site remains unaudited on that list: the tuple cross-block reuse path
(`emit_reuse_recip_prior_release`), a bare cow-guarded `__fern_rc_dec` of the
recipient's prior box with no element release, whose donor path also zeroes the
donor slot. Predicted to strand one source buffer per iteration for the same
reason; not measured.

### Two false leads recorded, because both cost time

- **"Nested blocks deny the credit."** Wrong. A block is irrelevant — an `if`
  *before* the tuple is clean, and a plain extra statement with no block at all
  leaks. Every probe behind that reading happened to use a `var acc = …` binding.
- **"It is the new `TUPELEMOK:` gate over-denying."** Also wrong, and it looked
  compelling because `frees` showed the box being freed, which does prove `TUP:`
  is granted. Rebuilt with the gate removed: still leaks. The gate is not on this
  path at all.

Both were settled the same way, and only that way: rebuild with one thing changed
and measure. Neither survived contact with that test.

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

None of the three is a regression, but note the reasoning that is NOT available
for saying so. "This change only adds decs, so it cannot regress anything" is what
I concluded while triaging the leaks above, and it is **false** — the extraction
over-release was an added dec. Adding a dec is safe only where the matching inc is
provably still outstanding, which is precisely what the credit gates decide. Each
of the three above is instead a leak the change leaves untouched, checked
individually with the underflow counter reading 0.

## Trap

The recorded negative result on #7226 (widening `tuple_arg_payload_fresh` +
`tuple_lit_has_rc_child` → exit 107) is real but its cause is class overlap, not
"the element side cannot carry the release". `tuple_lit_is_fresh_scalar` accepts a
bare ident **purely syntactically**, so the widened slot held `TUP:` *and*
`TUPRC:` at once and the **tuple box** was dec'd twice — not the element the fix
was aiming at. `live_bytes` 8000 → 7960 is that box, freed once. Keeping the slot
in exactly one class is what makes this change safe, and it is why neither gate is
touched here.
