# The retain choke point — promotion step 1, retain side

Step 1 of `SELFHOST-RC-PLAN-PROMOTION`: one emitter per direction, site key
as a required parameter. This is the retain half; the release half (its ~148
emissions already ~two-thirds routed through ~50 dedicated helpers) is the
next slice.

## What landed

Two receiver methods on `LowerState`, now the only spellers of
`__fern_rc_inc` for the mechanical retain sites:

- `retain(slot, site)` — the slot idiom: `load_local / rc_inc / drop`,
  stack-neutral.
- `retain_tos(site)` — the stack-top idiom: a bare `rc_inc` on the operand
  already on the stack (`__fern_rc_inc` returns its argument, so the
  consuming op's operand order is untouched).

`site` follows the #7349 convention — required, never derived inside, unread
by the emitters today. A binding-served retain passes its
`reclaim_site_key_of` key; a non-binding retain passes a class tag naming
the Perceus trigger (`"ret-field"`, `"append-owned-arg"`, …). Step 2 gates
the emission on the plan's verdict keyed by exactly this parameter, without
touching the call sites again.

24 sites route through the emitters: 17 stack-top (construction stores,
base copies, return transfers, container stores), 4 slot triples (TRMC
consume-arg, closure alias bind, the two append escapes), and the three
verbatim-identical `alias_inc` arms of `emit_arr_store` /
`emit_str_reclaim_store` / `emit_strarr_reclaim_store`.

## What deliberately did NOT move (the emitter's comment lists these)

Seven direct emissions remain, each entangled with its surrounding
emission: the manual `__rc_inc` passthrough, the enum ctor-payload dup and
its move exemptions, the struct-lit field-override retain paired with
move-elision bookkeeping, the element-outlives-buffer interleave, the
closure-env capture loop, and the two reuse emitters' donor-field /
runtime-conditional retains. The per-backend `map_set` vretain never
appears in irlower at all — its decision is an op flag, its emission lives
in each backend. These move in later slices, each with its pairing intact.

## The oracle

The refactor claims byte identity, and the standing fixpoints cannot prove
that (self-referential: gen0 == gen1 holds for any self-consistent
compiler). The proof used here: build the driver from the pre-change
sources and from the post-change sources, run both drivers' per-module
emit-all over the SAME pre-change snapshot project, byte-compare every
unit — the old-vs-new discipline `TYPED-IR-REWRITE.md` prescribes, on the
`runEmitAllFixpoint` machinery. All units identical.
