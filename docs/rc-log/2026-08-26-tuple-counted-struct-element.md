# The tuple's LIVE struct element, and the predicate this keeps happening to

`tuple__live` is clean. That is the fourth container in a row whose fix was the
same missing construction retain, and the repetition is the point of this entry.

## `slot_is_rc_container` is not "is this rc-tracked"

Every construction-store retain in the lowering asks some version of "is this
bare ident an rc-tracked local I must count?". Four of them asked
`slot_is_rc_container`, which is `is_arr || is_str || tuple_elems.len() > 0`.
A STRUCT box is none of those — its release is a field walk PLUS a box dec
rather than a box dec — so every one of those sites silently under-retained a
struct payload:

| site | fixed by |
|---|---|
| array literal element | #7503 (its own struct clause) |
| `Some(p)` / `Ok(p)` payload | #7528 (`opt_payload_struct_slot`) |
| variant ctor payload | #7531 (its own clause) |
| **tuple literal element** | **here** |

Three of those four were found by a probe going wrong, not by anyone looking at
the predicate. The name reads like a general rc-tracked test and is used as one;
it is actually "kinds whose release IS the box dec". Anything reaching for it to
decide a retain has the same bug waiting.

The remaining consumer is the tuple-element KIND recording, which is a different
question (which release shape does this element need) and correctly distinguishes
`a` from `t`.

## This slice

Narrower than the last three, because #7513 had already built the release: the
`t` element kind routes `emit_tup_struct_elem_release`, which walks the element's
fields under `__fern_rc_is_unique` and decs the box. It only lacked owners.

- The literal retains a bare-ident struct element, skipped at a MOVE site (there
  the tuple takes over the reference and its walk is the one release — what
  `tuple__moved` has measured since #7513).
- The `t` kind is recorded for a LIVE element too, not just a moved one. The
  comment #7513 left at that arm said exactly what was missing: "A source that
  stays LIVE is NOT recorded. The store holds no counted reference to it."  It
  does now.
- `struct_box_sink_stored` stops refusing a retained element, and
  `struct_counted_share_expr`'s tuple arm marks the source `SINKSHARE:`. That arm
  existed but only RECURSED into elements, so a bare ident — the whole shape here
  — matched nothing.

## The rc plan was already right, and that was worth checking

#7531 turned green partly off a GAP in `free_eligible_sites_of` rather than off
its fix, so the plan is now something to verify rather than assume. Here it holds
up: `rc_fe_walk_expr`'s `ExprTuple` arm already classifies every element with
`rc_fe_escape_owned` (counted, not a full escape), which is the correct
classification once the literal retains. No plan change, and the cell is green
for the reason it claims to be.

## Measured

`tuple__live` leak → clean; `tuplit_elem` 300/100 → 300/300. `tuple__moved`
unchanged. Every other probe in the corpus unchanged, every exit code matches
native, nothing exits 99.

A build failure hid behind `tail` in the probe script during this slice — the
compile error scrolled past and the probes then ran on STALE binaries and
reported "no change". `mk.sh` now redirects and greps, which is the rule
docs/TEST-GATES.md states for test runs and applies just as well to builds.

## Still open

`with__moved` / `with__live` — `arr_set` stores no counted reference, and `with`
is not a sanctioned arrstruct rebind either, so it needs both halves. And
`option__moved` / `option__live`, the match-EXPRESSION gap from #7528, which
needs a release PLACEMENT rather than a predicate.
