# the string[] alias joins the #7282 family — and #7391's premise was stale

#7391, the fourth limb of the #7282 alias-credit line (string c5c8ab4, tuple
dd80eda, container c0618b0, struct 0c98bf0). Leak matrix rows
`str_arr__fnscope__alias_local` / `str_arr__if_block__alias_local` flip
leak→clean; the alias_param rows stay refused (a parameter owns nothing to
share).

## The premise check that changed the design

The issue prescribed a DEEP retain (or a buffer-rc-gate on the release),
reasoning that `__fern_str_arr_free` "walks the ELEMENT boxes unconditionally",
which would make the shallow c5c8ab4 recipe a UAF factory. **That was a
mis-read**: the release has been buffer-rc-gated since #7292 (`24935fe`) —
rc>1 decs and leaves the elements to the surviving owner; only the LAST
owner's rc==1 walks them. Filed 2026-08-23, gate landed 2026-08-22. The
CLAUDE.md tracker-lag rule ("check the code, not the issue") is not just for
*done* work — an issue's *analysis* lags too.

So the fix is the ordinary shallow duplication, and the gate is what licenses
the one deviation from the family pattern: the alias takes the **same deep
"SARR:" class** the source holds, where a struct alias must take box-only
"NODEEP:". A struct's deep release (field walk + box dec) is not arbitrated
per owner, so two deep credits walk twice; `__fern_str_arr_free` arbitrates
internally, so two "SARR:" credits cannot.

## The diff

- `strarr_unsafe_for` grows an `_alias` variant carrying the #7282
  forgiveness list (the `body_unsafe_for_clo_alias` pattern); the plain name
  delegates with an empty list, so the SARRB arm is untouched.
- `strarr_alias_bind_sites_of` — `alias_bind_sites_of` with one substitution:
  the alias vets through the **strarr gate**, not `body_unsafe_for`, because
  a string[]'s release walks the elements — an element escaping from the
  alias (`var e = x[0]`) is invisible to the plain walker and would dangle
  under the deep free.
- The "SARR:" arm grants `credit_alias_sites(out, "SARR:", sal)` when the
  source passes with the forgiveness.
- `emit_strarr_reclaim_store` gains `alias_inc` (the c5c8ab4 hole, verbatim:
  the bind computed the retain, the credited slot's store route dropped it).
  The retain itself was already firing — `is_arr_slot` aliases have inc'd at
  the bind since before this family existed — which is why the pre-fix census
  was 500/100 rather than 500/0: the shallow decs returned the buffer at
  rc 0 while both element boxes and their data leaked, 64 B/round.

## Non-vacuity

Six rows in `self_host_container_alias_bind_test.go` (`strarr_alias*`), all
three backends: granted — fnscope, if-block, and an ELEMENT-BYTES read
through both slots before the sweep (a premature element free is a wrong
answer, a double walk is exit 99); refused — chain, element-bind-from-alias
(the hazard that forces the strarr-gate vetting), parameter source. Wants
oracle-confirmed; counts measured through the harness. The matrix rows are
the differential gate; SARRB (split/lines) aliases stay refused — a sound
leak, and a follow-up if a real program hits it.
