# STRUCT routes through the plan — step 2 wave 3

The struct family's escape conjunct (`body_unsafe_for_alias`, shared by the
struct-literal fresh and fresh-ret-call credits in `reclaimable_names_of`)
becomes the plan's per-site verdict under `FERN_SELFHOST_RC_PLAN`. The other
conjuncts stay family knowledge: the alias-rebind borrow flag
(`reassigned_from_alias`) and the two receiver-handed-back gates. The
alias-bind retain needed no edit: it was already keyed on
`slot_is_reclaimable_struct` — the credit lookup — so the #7253 co-extensive
pair moves with the credit by construction. The alias forgiveness is the
plan's own #7282 arithmetic, sound here because the struct alias bind
retains (unlike the Option kinds).

## The invariant the routing broke, and the shape of the fix

Pre-routing, `body_unsafe_for` refused every container-stored struct, so a
credited box was sole-owned at each of its release sites — and the struct
release protocol leans on that: the rebind `__field_reclaim_<T>` and the
exit sweep walk fields UNCONDITIONALLY, rc-blind. The plan's counted-sink
forgiveness (#7345) legitimately shares a credited box with a container,
and the deep walk then frees a field the container still reaches. Measured
on the append-rebind probe as exit 99: allocs=800 frees=600 with the
underflow tripped, against a clean plan-off baseline.

Three pieces close it, all credit-gated (#7253 in both switch positions):

- **The counted sinks retain a struct-credited bare-ident element** — the
  append arm (the SCENUMS arm's sibling), the array-literal element, and
  the tuple-literal element. The struct-literal field store already
  retained (#3292/#4579's arm covers struct fields).
- **A whole-box counted-sink store marks the slot NODEEP**
  (`struct_box_sink_stored`): the container co-owns the box, so this
  slot's releases are box-only and the container's own deep free drops
  fields exactly once. The field-move detectors could not see this shape —
  a bare ident is a leaf there — because the old escape gate made it
  unreachable for credited names.
- The box's rc arbitrates everything else, which is native's model.

## Measured (x86-64, census + underflow; exits match native on all probes)

| probe | native | plan-off (pre-wave) | plan-on |
|---|---|---|---|
| append + rebind + alias (the diff-table shape) | clean | clean | clean |
| alias of a param, read-only callee | clean | leak | **clean** |
| struct-lit field sink, loop rebind | clean | leak 30400 | leak 16000 |
| array-literal element sink | clean | leak 26400 | leak 26400 |
| tuple-literal element sink | clean | leak 26400 | leak 26400 |
| variant-ctor payload sink | clean | leak (all) | leak (all) |
| shadow sibling: fresh + param alias | clean | clean | clean |

Monotone: two shapes improve, none regress, no underflow anywhere. The
array/tuple-literal floors are box-retained leaks, never dangles — the
container holds a counted reference and no deep walk fires twice; closing
them needs the container-side element walk for ident-built literals, a
separate port. The variant-payload floor is the pre-existing OPTSTRUCT
fresh-literal-only rule; the plan taints uncounted variant payloads
(native parity), so nothing new is granted there.

The alias_param win flips the two `struct_arr_field__*__alias_param`
matrix rows to clean — the old gate refused main's `keep` for being a
call arg; the plan does not taint a plain call arg, and the read-only
callee never stores it.

The rcplan diff's `dead-alias-struct-loop-body-moved-source-excluded`
aliasBindIncs pin converged to an anchor (`8:3=v` both sides) — the
routing closed exactly the "retain-plan port gap" its comment recorded.
nestedDrops stays the placement divergence.

## CI-caught: the borrowed-field literal — a second sole-owner assumption

The wasm whole-compiler leg faulted in `flatten__lookup_ivar` at address
0x6661656c — string bytes ("leaf") read as a pointer. The shape:
`rewrite_module_bodies` rebuilds `RewriteCtx { ivar_names: ctx.ivar_names,
… }` per loop iteration; the plan grants the fresh literal (its call-arg
uses don't taint), and the deep walk then decs the caller's arrays every
iteration — the construction retain arms cover nested-struct / enum /
scalar-array / struct-array field kinds, but a string / string[] /
enum-array field READ reaches the new box uncounted. Loud on wasm's
recycling allocator, latent on the x86 freelist (the stage-2 x86 fixpoint
passed over the same code), and — the probe's finding — already latent
plan-OFF for a borrowable callee, where the old gate granted the same
literal. Fix in the same NODEEP pass, both switch positions: a candidate
whose literal carries a borrow-shaped value in an un-retained rc field
kind (`struct_lit_unretained_borrow_field`, the qualification list
mirrored from fieldmove_selfrebind_alias) takes box-only releases. The
wasm whole-compiler link and the full battery are green with it; the
probe census is unchanged (the census cannot see the removed
free-under-caller — the wasm run is the discriminator).
