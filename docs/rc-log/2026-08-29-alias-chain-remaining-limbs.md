# The alias chain in the other limbs: strarr, rc-enum, Option

#7750, the follow-up to `2026-08-29-alias-chain-credit.md`. That entry credited
the chain in the string and struct limbs, which share `alias_bind_sites_of`.
Three of the four limbs that keep their own alias-site walker are done here; the
tuple pair is not, for the reason that entry recorded.

## Measured

x86-64, `FERN_LEAKCHECK=1`, 100 rounds, `__rc_underflow()`-gated, `bin/fern
-interp` and the native x86-64 backend agreeing on every exit code. Shape is
`var t = <fresh>; var v = t; var u = v;`.

| limb | shape | native | before | after |
| --- | --- | --- | --- | --- |
| strarr | `string[]` | 100/100/0 | **500/100 live 6400** | 500/500/0 |
| rc enum | `E.A(i32[])`, matched | 200/200/0 | **200/0 live 8000** | 200/200/0 |
| rc enum | `E.A(i32[])`, dead chain | 200/200/0 | **200/0 live 8000** | 200/200/0 |
| Option | `Option[i32[]]` | 200/200/0 | **200/0 live 8000** | 200/200/0 |
| Option | `Option[string]` | 100/100/0 | **300/0 live 7200** | 300/300/0 |
| Option | `Option[Option[string]]` | 200/200/0 | **400/0 live 11200** | 400/400/0 |
| Option | chain in an if-arm | — | **200/0** | 200/200/0 |

Refusals that must hold, and do:

| shape | after |
| --- | --- |
| strarr, ELEMENT escapes from a middle link | 300/100 — refused |
| rc enum, a link hands the PAYLOAD out | 300/100 — refused |
| Option, a link hands the PAYLOAD out | 300/200 — refused |
| Option / strarr / string, last link RETURNED | 200/0, 500/0 — refused |

## The three limbs are not the same change

**strarr** is the closest to the shared one: closure via
`bare_alias_bind_sites_of`, then the set vetted through `strarr_unsafe_for_alias`
rather than the plain walker, because a `string[]`'s release WALKS THE ELEMENTS
and an element escaping from any link is invisible to `body_unsafe_for`.

**rc enum** is the cheap one, and worth saying why. Its limb uses the alias sites
for ESCAPE FORGIVENESS only — a confined link takes no release, so the source
stays the sole releaser and the box is freed once however long the chain is. No
link gains a dec, so the retain/credit arithmetic the string limb has to reason
about does not arise. Vetting is still the enum one (`body_unsafe_for_enumfield_alias`
plus `enum_body_binds_rc_payload`), because the release deep-drops the payload.

**Option** needed a walker change, and that is the substantive part of this entry.

## `body_unsafe_for_match_borrow` grew an `alias_ok`

The Option vetting pairs `body_unsafe_for_match_borrow` with
`!opt_body_binds_rc_payload`: the match-borrow reading is what admits an alias
that is itself matched, which is the commonest use of one. But that walker had
no `alias_ok`, so it flagged `var u = v` and refused every chain — the credit
simply did not take, and the first build measured unchanged at `200/0`.

The forgiveness mechanism was already reachable: the walker's `_` fallback arm
routes a `StmtVar` to `stmt_unsafe_for_alias_vb` with an EMPTY alias list.
Threading the parameter through `body_unsafe_for_match_borrow` /
`stmt_unsafe_for_match_borrow` and passing it there is the whole change; the
eight other callers pass `[]` and are byte-unchanged. No new match arms, so the
feature-census ratchet does not move.

The gate's count comparison had to follow: it asked whether every DIRECT bind of
the name was vetted, and now asks it of the whole closure
(`opt_alias_chain_closure`).

## Still refused: the tuple pair

Unchanged from the previous entry, and unchanged in reasoning. Both tuple limbs
perform move-on-alias credit surgery — the deep `"TUPRCS:"` class migrates to the
alias row at a move — and under a chain credit
`var t: (i32, i32[]) = …; var v = t; var u = v;` measures `__rc_underflow() != 0`
with a census reading a clean `200/200 live_bytes 0`. `FERN_RC_TRACE=1` shows one
retain for two links. `tuple_alias_chain_refused` still pins it; #7750 keeps it.

## Gate

`self_host_container_alias_bind_test.go` gains `strarr_alias_chain` (moved from
`…_refused`), `strarr_alias_chain_elem_escape_refused`, `enum_alias_chain` and
`enum_alias_chain_payload_out_refused`. `self_host_opt_alias_bind_test.go` gains
seven rows — chain, three links, matched, string payload, nested Option,
conditional, and two refusals — each with the `FERN_SANITIZE=1` leg that suite
runs on every row.

The refusal rows are not decoration here. All-or-nothing is what makes the set
sound, and an escape in the MIDDLE of a chain is the case a per-site rule would
have got wrong in the unsafe direction.
