# A tuple's struct element gets a release — the moved half

`tuple__moved` on the container-sink matrix: `var tp: (i32, P) = (i, p);` with `p`
not mentioned again. 300 allocs / 100 frees over 100 rounds against native's
300/300 — the tuple box was freed and the struct element it held was not, along
with that struct's array field.

## Why it recorded nothing

A "TUP:" tuple local records per-element rc kinds (`add_tup_elem_kinds`), written
from the SAME `slot_is_rc_container` / `is_str_slot` test that decides the
construction retain, which is what keeps the retain and the release it owes on one
predicate. A struct box is in neither: its release is a deep FIELD walk plus a box
dec, not the box dec those two kinds mean. So the position recorded `.` — nothing
to release — and the element leaked.

## What it records now

A struct element the store MOVES takes a kind of its own, `t`. The type comes from
the slot's tuple TAGS (`tuple_elem_tag`), so the kinds string stays one char per
position and no consumer's encoding moves; only `emit_tup_elem_releases` gains an
arm, and it emits the array family's protocol — `emit_struct_field_drops_gated`
then an unconditional `__fern_rc_dec`. A scalar-field struct routes no field
reclaim, so the gate emits nothing and the box dec is the whole release.

## Why only the moved half

The LIVE element (`tuple__live` — the source read after the store) is deliberately
not recorded, and the distinction is the whole soundness argument. `tuple_make`
holds no counted reference to a struct box, so for a live source the walk here
would free memory that source still reads: a dangle, not a leak. A move makes the
tuple the sole owner, and then the walk IS the one release.

Closing the live half needs what the array family got: the tuple credit stamping
its bare-ident element sites, the tuple-literal lowering retaining at the stamped
non-moved ones, and `struct_box_sink_stored` / `struct_counted_share_expr` reading
those stamps so the source keeps its credit and both owners release under the rc
gate. That is a bigger move here than it was there, because the "TUP:" credit is
computed AFTER the struct family's gate in `reclaimable_names_of` and would have to
be hoisted the way `arrstruct_credit_rows` was.

## Measured, x86, 100 rounds, against native

| cell | before | after |
|---|---|---|
| `tuple__moved` | 300 / 100 | **300 / 300** (native 300/300) |
| `tuple__live` | leak | leak (refused) |

Exit codes match native on both, `__rc_underflow_count()` is 0. Stashing the
compiler change fails exactly `tuple__moved` and no other cell.
