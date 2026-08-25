# A variant's MOVED struct payload gets its release, and both spellings agree

`variant__moved` on the container-sink matrix: `var e: E = E.A(p);` with `p` not
mentioned again. The enum family already had the whole release protocol —
`emit_enum_variant_drops_gated` puts the payload walk under `__fern_rc_is_unique`
with the box dec unconditional, and `emit_enum_variant_payload_drops` has both a
deep-drop-ok struct arm and a scalar-only-struct arm. What refused this shape was
the ADMISSION, `variant_struct_payloads_fresh`, which demands a fresh struct
LITERAL for every struct payload.

## Its stated reason did not apply

> a bare-ident payload aliases a local whose own sweep would double-free the box

There is no such sweep: `struct_box_sink_stored_expr` refuses a variant-ctor
argument its struct credit, so the source releases nothing. What a MOVE actually
adds is that the source's VALUE is dead after the store, which is what makes the
enum the only reader and its payload drop the one release. The new comment says
that rather than inheriting the fresh-literal arm's wording.

## The spelling bug this uncovered

The two ctor spellings got different rc analysis, and the difference was a latent
over-release. `struct_box_sink_stored_expr`'s ExprFieldAccess arm knew only
`append` and `with`, so the QUALIFIED `E.A(p)` was never seen as a counted sink
while the bare `A(p)` was — same program, 300/200 one way and 300/0 the other.
`fresh_rcpayload_enum_init` has carried both spellings all along.

On the qualified spelling `p` therefore kept its struct credit and was swept.
Nothing double-freed only because the enum had no payload drop for a bare-ident
payload — and this slice adds exactly that. So the arm had to learn the qualified
ctor in the same commit; without it, `p`'s sweep and the enum's drop would both
release the same box.

That fix costs the LIVE qualified shape its partial reclaim (300/200 -> 300/0,
still a `leak` verdict either way). Sound, and the right trade against an
over-release; closing it properly is the `variant__live` slice, which needs the
ctor to hold a counted reference the way the array family's stores now do.

## Three passes, and the one that mattered

`fresh_rcpayload_enum_init` has eight callers. Threading the move set into
`precise_drop_names` and `consumed_rcpayload_enum_frees` moved nothing — the
probe's match is a match EXPRESSION inside a `return`, so `body_has_top_level_match`
is false and `sole_top_level_match_idx` never finds it. The pass that grants this
shape its credit is `collect_fresh_rcenum_names`, whose "RCENUM:" / "RCENUMS:" rows
drive the reclaim. The other five callers keep `[]`: a struct-literal field, an
array element and a return ask a different question, and the rebind-chain pair is
its own admission.

Three edits in a row measured identically to no edit at all before that was found.
The census cannot tell "wrong fix" from "incomplete fix", so the way out is an
instrument, not a fourth guess — see the enum-ctor construction-move entry, whose
rcplan-diff case is what proved the move was missing in the first place.

## Measured, x86, 100 rounds, against native

| cell | before | after |
|---|---|---|
| `variant__moved` | 300 / 200 | **300 / 300** |
| `variant_unqual__moved` | 300 / 0 | **300 / 300** |
| `variant__live` | 300 / 200 | 300 / 0 (leak either way) |
| `variant_unqual__live` | 300 / 0 | 300 / 0 |

Native is 300/300 on all four. Exit codes match and `__rc_underflow_count()` is 0.
Stashing the compiler change fails exactly the two moved cells.

The grid now pins the bare spelling as its own position. Nothing in it used the
unqualified form before, which is why the disagreement went unnoticed.
