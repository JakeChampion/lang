# 2026-09-02 — a reused box's old `Cell` field was dec'd, never reclaimed

The residual left by `2026-09-02-cell-container-child-drop.md`, at the fourth
and last of the sites that route a child drop. The first three are the
generated drop fns (`appendChildDrop`), the function-exit sweep
(`dropStructField`), and a slot reinit. This one is Perceus REUSE.

## What was actually leaking

When reuse hands a dead value's box to the next construction, the reuse branch
first releases the previous occupant's pointer fields, so the new stores do not
strand them. All three reuse paths — struct overwrite, enum overwrite, and the
cross-value `emitReuseOldFieldDrops` — do that through
`emitFieldDropOnStack`, which delegated an ARRAY field to `dropStructField`'s
ladder and left everything else to `dropFnNameFor`. That declines `Cell` (no
generated drop fn exists for it), so a cell field fell to the flat
`__fern_rc_dec`: decrement, return, box stranded.

The fix is one line of routing — a `Cell` takes `dropStructField` outright,
exactly as an array already did, because a cell IS a one-element array box.

## Why it looked like it scaled with the number of containers

The earlier entry recorded this as "N containers strand N − 1 slot buffers"
and blamed slot accounting. Both halves were wrong, and the shape of the
measurement is what misled: only the FIRST container in a function allocates a
fresh box, and every one after it reuses a dead predecessor's. So the leak
count tracked reuse sites, not cells, not accumulation, and not string slots.

The discriminator that settles it is that each container in isolation was
already clean, and any PAIR of them leaked exactly 1 — regardless of which two:

| blocks from `cell_in_container` in one `main` | unpaired |
|-----------------------------------------------|----------|
| tuple + scalar cell, alone                     | 0        |
| enum + string cell payload, alone              | 0        |
| one cell shared by two tuples, alone           | 0        |
| any two of the three                           | 1        |
| all three                                      | 2        |

Reading the emitted x86-64 alongside an `FERN_RC_TRACE` pairing is what named
it: the unpaired block's only release was a bare `__fern_rc_dec` on the stale
field, emitted immediately after an `__alloc_reuse` under its `is_unique`
guard.

## Measured

Conformance census, x86-64, unpaired allocations. Two rows moved and nothing
else in the corpus changed:

| fixture                  | before | after |
|--------------------------|--------|-------|
| `cell_in_container`      | 2      | 0     |
| `cell_struct_field_wide` | 3      | 0     |

## Why freeing here is safe

The same argument as the exit-sweep arm beside it: `__fern_arr_dec` and
`__fern_drop_arr_str` walk and free only at the cell's OWN rc == 1, so a cell
anything else still holds is merely decremented. The reuse branch is
additionally gated on the box's `is_unique`, and the rc arithmetic is
unchanged — still exactly one dec per replaced field. Only the rc-0 free is
added.

## Still open

A cell as an ARRAY ELEMENT still strands 1, unchanged by this: see the
"Also still open" section of `2026-09-02-cell-container-child-drop.md`.
