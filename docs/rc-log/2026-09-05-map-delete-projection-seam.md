# 2026-09-05 — the projected delete tuple: four things wrong at one site

`sm = sm.without(k).0` was the last of the five `m.without(k)` corpus shapes
still leaking, at 128000 / 144000 / 104000 B over 500 rounds. It needed four
changes, and **each one measures as a no-op without the others** — which is why
two earlier attempts at it read as dead ends.

## What the IR actually showed

Dumping `mk`'s ops rather than reasoning about the analysis:

```
 57 const.i32  20
 58 alloc                  <- the result tuple's box
 …
 81 rc.inc                 <- the projection retains element 0
 82 local.load 1
 83 rc.dec                 <- FLAT dec of the old sm
 …
 98 call __drop_struct_flat_Map   <- FLAT exit drop
```

Two faults visible at once. The tuple box is allocated and **never freed** —
no `__drop_tuple_`, no `__fern_box_free`. And `sm`'s releases are both flat, so
the kv buffer and the key column are never walked: `freeEligible[sm]` is false.

## The four

1. **`computeMapCowBindSites` gains a `*ast.FieldAccess` arm.** A delete call
   that is the TARGET of a projection owes the COW-seam retain exactly as a
   bound one does. The field doc had this backwards: it listed
   `m = m.without(k).0` among the positions that must NOT retain, on the
   grounds that the result is "a temporary nobody binds". The right test is not
   whether anything BINDS it but whether anything DROPS it.

2. **That analysis moved above `computeFreeEligible`.** It ran at
   `computeRcAnalyses` line 351; `freeEligible` at 338. Any gate on
   `mapCowBindSites` inside `rhsTainted` therefore read an **empty map**. This
   is what made the credit look like a no-op twice: the lowering (which runs
   later) saw the retain and dropped the box, the analysis never did.

3. **`freshOwnedFieldContainerType` admits a seam-retained delete tuple**, and
   the `!isMapType` guard on `rhsTainted`'s fresh-container arm goes with it.
   Restoring that guard alone puts 112000 back, so it is load-bearing rather
   than incidental.

4. **The general one.** The `*ast.Assign` alias-inc path never had the
   `!isOwnedContainerRead` guard the `*ast.Var` path has carried since #6401.
   So `a = mk().items` took **two** retains — the container read's own, plus
   the alias inc — against one exit dec, pinning the field at rc 1 forever.
   This is not map-specific; it applies to every rc type read out of a fresh
   owned container and rebound by assignment, and has its own issue (#8599)
   so it is findable from an array or string leak rather than only under a
   Map ticket.

Piece 4 is what takes the case to zero. With 1–3 alone it stops at 112000 /
128000 / 96000: the box comes back (32 B a round on the natives, 16 on wasm32)
and the table still does not.

## Measured

| case | x86-64 | arm64 | wasm32 |
|---|--:|--:|--:|
| `map_delete_projected_self_assign_churn_free` | 128000 → **0** | 144000 → **0** | 104000 → **0** |
| `map_delete_projected_call_arg_churn_free` (new) | 16000 → **0** | 16000 → **0** | 8000 → **0** |

The second is a case rather than a pin: `sizeof(m.without(k).0)` leaked only
the undropped tuple box, small enough that no existing fixture noticed it. It
also guards the boundary from the leaking side — `sizeof(m.insert(k, v))` next
door still must not retain, because nothing drops that one.

All five `m.without(k)` shapes now reclaim completely and none of them appears
in the leak tables. #8434 is closed.

## The test that had to change

`TestMapCowRetainOnlyAtBindingSites` failed, and correctly: it pinned
`projected` at one retain on the stated grounds that a retain on a temporary
"leaks a whole table per evaluation". That was true while nothing dropped the
temporary. The premise, not the assertion, is what moved — so the fix is a
rewritten rationale and `projected` moved into the retaining table, plus a new
`argproj` row for the call-argument spelling the test never covered.
