# The array-LITERAL element joins the counted sinks, and the matrix says who is left

Two changes landed earlier today made a self-appended struct element a counted
share: `arrstruct-live-element` widened the ARRSTRUCT admission and stamped
`"APRETAIN:"` at the sites that retain, and `field-reclaim-shared-box` rc-gated
the rebind so a shared box's fields survive it. This slice finishes the ARRAY
family — the LITERAL spelling `var ps: P[] = [p]` had none of it — and adds the
grid that says which container positions are still open.

## What was still leaking

Measured x86, 100 rounds, against native, on the state those two changes left:

| shape | self-host | native |
|---|---|---|
| `var ps: P[] = [p]`, p read after | 300 / 100 | 300 / 300 |
| `ps.append(p); p = P { … }; ps.append(p)` | 600 / 400 | 600 / 600 |

The literal is the plainer of the two: `arrstruct_lit_is_fresh` demanded a fresh
struct literal per element, so a bare ident refused the CONTAINER its element
walk, and the struct gate refused the SOURCE its credit — the array-element sink
had no retain at all.

The second is subtler and is the one worth recording. `arrstruct_owned_elem_sites`
(then `arrstruct_append_retain_sites`) skipped a site the move analysis had taken
over, because a move needs no retain. But the same stamps are what tell the
struct gate that an append is a counted share rather than an uncounted sink — so
at the MOVED push the gate saw an uncounted sink and refused the source outright.
The first, retained, push then had no owner left to release it: the container
dec'd the box once and nothing dec'd the retain.

## The separation that fixes it

A stamp now says **who owns the element**, and nothing else. Whether a given
store RETAINS or TRANSFERS is the move analysis's answer, read at the lowering
site, which already has `moves_local_at`. Encoding both in one stamp is what
entangled the two questions.

So `arrstruct_owned_elem_sites` stamps every bare-ident element site of a
credited container — appends and literal elements alike, moved or not — and both
lowering sites do the same thing with it: retain, or `note_moved_elided` and let
the container's walk be the one release.

## The gate that was missing, and the instrument that found it

Granting the literal its credit without also marking the source `"SINKSHARE:"`
exits 99 — an over-release with allocs == frees at `live_bytes` 0, so only the
underflow counter dissents. Both owners were walking the fields: the container's
walk is rc-gated, the source's exit sweep is gated only for `"SINKSHARE:"` names,
and whichever ran at rc 1 first did the walk the other then repeated.
`struct_counted_share_expr` now reports the array-literal element alongside the
struct-literal field and the self-append, which is what narrows the gate to
exactly the names that need it.

## Measured, x86, 100 rounds, against native

`TestSelfHostContainerSinkMatrixX86_64` — CONTAINER POSITION x what the source
does after the store — pins all fifteen cells. Native is clean on every one.
Four flip: `arrlit__moved`, `arrlit__live`, `arrlit__rebound`, `append__rebound`.
Stashing the compiler change fails exactly those four and no others.

The grid is also the map of what is left. `with`, `tuple`, `variant` and
`option` leak in both shapes: those stores hold no counted reference to a struct
box, so `struct_box_sink_stored` refuses the source's credit rather than dangle.
Each is now the same mechanical slice this one was — stamp the container's
element sites, add the store's retain, admit the counted element in that
container's own credit, flip its two cells.

## Converged with native

`dead-alias-struct-loop-body-moved-source-excluded` in the rcplan diff loses its
`aliasBindIncs` divergence: with the moved push stamped, p keeps its credit and
the alias bind retains, as native's does. The line moves from `diverge` to
`anchor`. `nestedDrops` stays divergent — the self-host dumps no counterpart for
that table, a gap in the dump rather than in the analysis.
