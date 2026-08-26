# The variant payload's counted-share half, and the rc plan that hid it

`variant__live` / `variant_unqual__live` were the last two `leak` cells in the
container-sink grid that a store-side fix could reach. Both are clean now, and
getting there needed a change in a place the slice did not look like it would
touch: the rc escape plan.

## The four parts

Same protocol as #7528, one container over:

1. **The retain.** `lower_variant_ctor_args` incs a bare-ident STRUCT payload of
   a releasable field type, skipping a MOVE site — native's
   `needsRcIncOnAlias && !moveSites`. The array-only gating it had is the same
   one `lower_opt_make_payload` carried before #7528.
2. **The gated release.** `emit_enum_variant_payload_drops`' struct arm ran
   `__struct_drop_<T>` unconditionally, on a stated invariant: admission only
   ever handed it a fresh struct literal, so rc == 1. The walk now runs under
   `__fern_rc_is_unique` and the dec stays unconditional. At rc 1 — fresh
   literal, or a MOVED payload — the guard passes and it is the release it always
   was.
3. **The credit.** `variant_struct_payloads_fresh` could not simply be widened
   the way `optstruct_init_is_fresh` was. It feeds `fresh_rcpayload_enum_init`,
   which has six consumers, and one — `struct_lit_all_enum_fields_fresh` — states
   its premise as "provably SOLE-OWNED by this struct (rc=1, no construction
   alias-inc)". A retained payload is rc 2 by definition, so admitting it there
   would license a deep-drop over a box someone else holds. The admission is a
   `counted` flag instead, true only at the two credits whose release is the
   gated emitter above and false at the three sole-ownership consumers.
4. **The source's own gate.** `struct_box_sink_stored` stops refusing a retained
   payload, and `struct_counted_share_expr` marks it `SINKSHARE:`.

Part 4's second half is not optional, and skipping it is measurable: with the
source credited but its sweep walking STATICALLY while the enum's walk is gated,
which of the two frees the buffers depends on which sweep runs first. Measured as
**exit 99** on a live payload, with `allocs == frees` at `live_bytes 0` and only
`__rc_underflow_count()` dissenting. Gating both makes the order irrelevant.

## The part that was not on the plan: the rc plan

With 1-4 in place the QUALIFIED spelling went clean and the bare one stayed at
300/100. Dropping the `plan_fe` requirement from the struct credit's escape gate
made both clean, which named the refuser: `free_eligible_sites_of`.

Its ctor arm taints a bare `A(p)`'s arguments — `escape_owned` when
`enum_all_variants_array_payload` says the enum's payloads are the counted
shape, a FULL escape otherwise. A struct payload was not that shape, so `p` was
tainted and refused. The qualified `E.A(p)` has no ctor arm at all: it fell
through untainted.

So the two spellings disagreed in the plan, and — this is the part worth
recording — **the qualified spelling's clean result was riding on the gap, not on
the fix**. Shipping 1-4 alone would have turned a cell green for the wrong
reason, and left it to revert silently the day someone gave the plan its missing
qualified arm.

Both spellings now go through one `rc_fe_variant_ctor_args`, and
`enum_payloads_counted_at_ctor` widens the counted shape to include the struct
payload the ctor now retains. The plan and the construction answer the same
question, which is the invariant `struct_routes_field_reclaim_at`'s header states
for the string-field store: a credit the retain does not honour is an
over-release, not a missed optimisation.

## Measured

| cell | before | after |
|---|---|---|
| `variant__live` | leak (300/0) | **clean** |
| `variant_unqual__live` | leak (300/0) | **clean** |

Every other probe in the 36-probe corpus is unchanged, every exit code matches
native, and nothing exits 99. The moved cells stay clean via elision — the retain
is skipped at their move sites, so they never became counted.

`variant_escape_uaf` stays a safe leak (200/500) and should: the enum escapes the
function, so nothing may release the payload there.

## Still open

`with__moved` / `with__live` (arr_set stores no counted reference), `tuple__live`
(tuple_make likewise), and `option__moved` / `option__live` — the last two being
the match-EXPRESSION gap #7528 recorded, which needs a release PLACEMENT rather
than a predicate.
