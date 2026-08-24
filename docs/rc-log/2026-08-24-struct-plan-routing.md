# STRUCT routes through the plan — step 2 wave 4

The struct family's escape conjunct (`body_unsafe_for_alias`, shared by the
struct-literal fresh and fresh-ret-call credits in `reclaimable_names_of`)
becomes the plan's per-site verdict under `FERN_SELFHOST_RC_PLAN`. The other
conjuncts stay family knowledge: the alias-rebind borrow flag
(`reassigned_from_alias`), the two receiver-handed-back gates, and — the
wave's own finding — a **counted-sink refusal** (`struct_box_sink_stored`).
The alias-bind retain needed no edit: it was already keyed on
`slot_is_reclaimable_struct` — the credit lookup — so the #7253
co-extensive pair moves with the credit by construction.

## Why the counted-sink forgiveness is NOT taken for structs

The plan keeps a container-stored name eligible (#7345) because natively
the store retains and fields drop at the box's rc-death. The self-host's
struct release protocol provides neither: roles are STATIC (the alias
holds box-only "NODEEP:", the source keeps the deep walk) and the walks
are rc-blind (`__field_reclaim_<T>`'s field dec, the sweep's
`__struct_drop_<T>`). Granting a container-stored struct measured as:

- exit 99 on the append-rebind probe (the rebind's field walk freed a
  field the container still reached), and after retro-fitting store
  retains + a sink-NODEEP role, as per-round leaks that flipped the
  `alloc_flat_*` fixtures to "grows" — the box's deep walk has no
  order-independent owner under static roles.

A full attempt at the coherent fix — unique-gated walks everywhere, the
"killer dec drops the fields" model, which IS native's — touched
`emit_struct_field_drops`, the rebind reinit, three backends'
`__field_reclaim_<T>`, and the alias NODEEP convention, and each probe
exposed another statically-roled site (inline element walks, read-path
reclaims). That is a release-protocol port with its own audit surface, not
a conjunct — so the routed gate refuses sink-stored names instead, exactly
the `name_is_match_scrutinee` pattern, and the refusal retires with the
**killer-drops-fields release-protocol wave**, not with keying. A
struct-lit BASE (`T { ...name, … }`) is not a sink at all — a field-copy
borrows the box rather than storing it, the moves_fields NODEEP marking
already handles its deep-drop hazard, and refusing it cost both the
NestedFieldAliasRebind exact-0 (self-rebind carry) and the fork_base
shape of alloc_flat_struct_self_update.

## CI-caught: the borrowed-field literal

The wasm whole-compiler leg faulted in `flatten__lookup_ivar` at
0x6661656c — string bytes ("leaf") read as a pointer. The shape:
`rewrite_module_bodies` rebuilds `RewriteCtx { ivar_names: ctx.ivar_names,
… }` per loop iteration; the plan grants the fresh literal (call args
don't taint), and the deep walk then decs the caller's arrays every
iteration — the construction retain arms cover nested-struct / enum /
scalar-array / struct-array field kinds, but a string / string[] /
enum-array field READ reaches the new box uncounted. Loud on wasm's
recycling allocator, latent on the x86 freelist (the stage-2 x86 fixpoint
passed over the same code), and already latent plan-OFF for a borrowable
callee. Fixed for both switch positions in the NODEEP pass: a candidate
whose own literal carries a borrow-shaped value in an un-retained rc
field kind goes box-only (`struct_lit_unretained_borrow_field`, the
qualification list mirrored from fieldmove_selfrebind_alias).

## Two rc_fe fidelity gaps the fixtures caught

Routing the struct family made the plan's verdict LOAD-BEARING for
call-bound struct locals, and the `alloc_flat_*` fixtures immediately
found two places the port was strictly more conservative than native —
each one refusing an entire fixture's worth of credits through the
any-tainted-arg call rule:

- **`rc_fe_rhs_tainted`'s binary arm** defaulted to tainted for every
  non-concat binary. A non-concat binary yields a SCALAR that cannot
  alias heap (and concat copies into a fresh buffer), so the arm is
  unconditionally false — the old default refused `mkw(i % 8)` through
  its own scalar argument.
- **The param taint seed** covered every non-own param regardless of
  type. A scalar param cannot alias heap either; seeding it poisoned
  `nodeB(s, n)` through n unless the callee happened to qualify for the
  noesc oracle. The seed now skips scalar-typed params; the rest of
  native's owned-by-default paramVerdict ladder stays unported.

Both changes move the DUMP, and the rcplan diff gate held with zero
movement on the corpus — pure convergence.

## Two more, CI-caught by the shard suites

- **The noesc subset was too narrow for producer chains** (ArrArgReclaim's
  `node(w(pre), deps_of(pre), n)`): `w` returns a concat and `deps_of` a
  builder local, both shapes native's oracle admits and the subset cut, so
  `f` was plan-refused and the #6522 counted-retain arg-temp dec lost the
  deep drop that balanced it. Widened, still subset-of-native: a `+` tree
  witnessed by a string literal is a fresh concat (bare-ident leaves of
  any type — they are copied); a BUILDER local (declared once from a
  fresh container literal, grown only by appends of admitted values) is
  returnable; and a still-qualified FUNCTION callee's args are irrelevant
  (its result aliases none of them — native's composition), while variant
  ctors keep the strict per-arg check ("fn:" rows split the two). The
  diff gate held.
- **The unretained-borrow NODEEP rule over-marked bare idents**
  (BorrowedFieldRetain: the caller lost `__struct_drop_H`). The
  construction's fallback retain admits bare IDENT string/string[] values
  — the strfld admission — so only the READ shapes (field access, index,
  slice) reach the box uncounted. The rule now marks reads only.

## Three uncounted escape channels, shard-caught (BlockScopedStructBox)

The plan forgives three more channels because native retains at them and
the self-host does not — each measured as an over-release the moment the
routed gate granted it: a bare-ASSIGNED source (`held = s`; plain
assignment retains nothing, want-frees-0 went 800), a bare RETURN
(`return s`; no return-transfer retain, the caller's fresh-ret release is
a second claim), and a bare arg to a HAND-BACK callee
(`held = keepit(s)`, keepit returning its param raw — underflow 244).
Three family-knowledge conjuncts refuse them
(`struct_bare_assigned_src`, `struct_returned_bare`,
`struct_arg_to_handback` over a new `handback_params` "fn|idx" registry —
only a DIRECT `return <param>` counts; a param stored into a returned
construction takes the construction's counted retain). All three retire
with the killer-drops release-protocol wave. The StructProducerLocal
sibling rows moved 200→204 frees with live 0 — main's `b` call arg is
now swept, the same caller-side win as alias_param; the refused rows
(param-returned, reassigned) hold at their pins through the conjuncts.

## Measured (x86-64, census + underflow; exits match native everywhere)

| probe | native | plan-off (pre-wave) | plan-on |
|---|---|---|---|
| alias of a param, read-only callee | clean | leak | **clean** |
| append + rebind + alias (sink → refused) | clean | clean | clean |
| struct-lit / array-lit / tuple-lit sink (refused) | clean | leak | leak (identical) |
| the alloc_flat composite (fixture shape) | flat/clean | flat/clean | flat/clean |
| RewriteCtx borrowed-field rebuild | clean | leak 136 | leak 136, box-only |
| shadow sibling: fresh + param alias | clean | clean | clean |

The alias_param win flips the two `struct_arr_field__*__alias_param`
matrix rows to clean, and moves two ContainerAliasBind rows: the
parameter-alias row goes clean on the caller side (the callee param
refusal it pins still holds), and the as-pattern binder's scalar-only
desugared chain balances (the rc-field chain stays refused). Every
sink-stored shape is byte-identical to plan-off. The rcplan diff's
loop-body-moved aliasBindIncs pin stays a divergence — its comment now
names the killer-drops port as the retirement condition.
