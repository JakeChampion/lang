# 2026-09-18 — a tuple box is storage too

Follow-on to `2026-09-18-a-box-is-slots-not-a-type.md`, which took the typed
path's reuse pairing from 147 firings to 640 by dropping the same-type test.
The pairing still spent a token at one construction only: `record_new`. The
probe behind that entry ranked what was left — 642 `tuple_new` with no donor
pending, 78 with one pending that was passed over for not being a record, and
538 tuple drops refused as donors before they could be taken.

A tuple box has no shape word. Element i is slot i, so the box is one word per
element and nothing at slot 0 identifies it — which makes it the simplest of
the three forms to hand on and the simplest to receive. `reuse_shape` admits a
tuple, `reuse_recipient` names the two constructions a token can be spent at,
and `reuse_tuple` writes the elements through `op_tuple_set` / `op_tuple_set_w`
at the width each element's kind names. That width is not optional: wasm
writes the low half of an eight-byte slot with the narrow form, and the read
back is garbage — the same rule `op_tuple_make_k` already carries on the fresh
path.

All three directions pair, because none of them is a type question: a tuple
donor to a tuple recipient, a record's box to a tuple, and a tuple's to a
record. The runtime's cap test decides, as it does for two records.

**An array is still refused as a donor**, and the reason is not its layout: its
cap is a CAPACITY rather than its length, so the slots the block has are not
the slots its type names, and a pairing would be admitted on a number the
static side cannot see. The runtime test would decline it correctly every time
it was wrong, which is to say the pairing would be noise.

## What came out with it

`drop_value` walked a box's children inline and `reuse_token` called a drop
HELPER, so the token's release was records and unions only. Nothing had
noticed, because nothing else could donate. Both read `drop_children` now —
the same walk, under the uniqueness test each caller emits for itself — so the
tuple donor's elements are released where a record's fields are.

## Measured

**Corrected by `2026-09-18-a-firing-is-not-a-reuse.md`, which reads these
counts as what they are: calls, of which 610 of 839 decline at run time.**

Firings of `__fern_alloc_reuse` compiling the compiler
(`bin/fern-selfhost -target x86-64-linux -emit asm examples/self_host/fern.fern`):

| path | firings |
|---|---|
| typed, before the cross-type pairing | 147 |
| typed, cross-type | 640 |
| typed, tuples too | **839** |
| AST lowering, same sources | 449 |

## Pinned

`TestSelfHostSemanticReuseDifferentialX86_64` gains `tuple-form`: the three
directions in one module, three firings on the typed path and zero on the AST
one, so the count cannot be satisfied by `irlower`.
`TestSelfHostSemanticSourceRC` gains the same shapes (`tuple_step`,
`tuple_loop`, `tuple_from_rec`, `rec_from_tuple`), balanced on all four
targets.

## Next lead

Re-probing after this is what found the correction above, and it moved the
lead: a second token slot is not the question while the donor a block holds is
chosen without asking what the construction in front of it needs.
