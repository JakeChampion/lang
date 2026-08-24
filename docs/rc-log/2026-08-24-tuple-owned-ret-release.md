# Tuple owned-return release — the p9 family

The #7464 review's "conditionally-returned rc-tuple leak" turned out to be
neither about conditionals nor about a corner: measured by probe, **every
function returning an rc-element tuple leaked at a bound call site** —
`var t = (i, [i, i+1]); return t;` gave the caller allocs=200 frees=0 per 100
rounds, and even direct-literal returns freed only the box (frees=100), where
native is clean on all of them. Arrays (`ARROWN:`) and structs
(`struct_ret_local_is_frame_fresh`) had their ret machinery; tuples had none.

## The mechanism (mirrors the array/struct precedents)

- `tuple_fresh_ret_fns_of` now admits bodies mixing direct tuple-literal
  returns with bare returns of FRAME-FRESH locals
  (`tuple_ret_local_is_frame_fresh`: declared once from a direct tuple
  literal, never reassigned, escaping only through the return — the
  `body_unsafe_for_alias_ret_ok` walker with empty borrow lists).
- `collect_tuple_ret_shapes` contributes the declaration literal's shape for
  a bare return, so the `ARRF:` per-position flags cover local-return paths.
- The caller's bound slot takes 'a'/'.' element kinds from the callee's
  flags, so the existing `TUP:` sweep give-back and loop-rebind reclaim free
  the flagged arrays before the box dec — **gated on `TUPELEMOK:`** (below).
- The tuple credit gates consult the ret-forgiving walker for names in
  `tuple_ret_local_names_of`, so a returned local earns `TUP:`/`TUPRC:`/
  `TUPRCS:` and is swept on non-returning paths; `returned_moved_arr_slots`
  gains the tuple branch that keeps it on the returning one (the sweeps were
  already keeps-aware).

## What the adversarial review caught before it shipped

- **Ungated caller kinds were a use-after-free** (both skeptics, measured
  independently): `var r = mk(i); return r.1;` had the pre-return sweep dec
  the array being returned — sanitizer exit 124, census silent (the double
  free balances). The literal-init kinds were always gated on `TUPELEMOK:`
  (the payload-escape credit); the call-bound kinds now take the same gate.
  Gated, the shape is fully CLEAN, not merely floor: the extracted array's
  one reference rides to the outer caller's slot, released by the `is_arr`
  slot-flag sweep no credit can deny.
- **The rc-tuple class's second escape scan had not been told**: the shared
  gate learned the bare-return forgiveness but `rctuple_payload_escapes_alias`
  still read `return t` as an escape, denying every ANNOTATED returned local
  its `TUPRC:`/`TUPRCS:` — swept on NO path (160 B/round on a loop-resident
  probe), exactly the #7282 lesson its own comment records. Fixed with
  `rctuple_payload_escapes_alias_ret_ok` (whole-tuple bare return forgiven;
  an ELEMENT return stays refused — that refusal is the UAF gate above).

## Table movements and witnesses

- `tuple_mixed__callprod__alias_local` flips leak→clean (the #7464 boundary
  row: the credit now exists, the alias pair cancels).
- New `tuple_mixed__ownedret_alias__bind_local` (clean | leak): an aliased
  producer local refuses the whole admission — pinned because forgiving
  aliases is the widening most tempting next, and a careless one flips this
  to over-release, not leak.
- New suite `self_host_tuple_owned_ret_release_test.go` (x86/arm64/wasm):
  eight heap-growth cases — the five p9 shapes with dynamically LIVE early
  arms (a dead arm made the keep-sweep half unverifiable), two-locals
  interplay, the loop-resident annotated callee, and the extraction boundary
  pinned clean.

## Boundary left as floor (refusals, all leak-direction, native clean)

Param returns, aliased-then-returned, field-read and forwarded-call returns,
tuple-returning methods (`receiver_type` gate), string-element positions
(flags mark scalar-elem arrays only), wide-scalar elements (decline the
whole `ARRF:` entry). The alias refusal is the pinned one; the rest follow
the same pattern if they ever matter.
