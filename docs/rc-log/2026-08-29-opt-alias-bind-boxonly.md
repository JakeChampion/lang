# The unmatched-Option alias: refusing the retain left nobody releasing

#7687's Option half. `opt_unmatched_esc_ok` carried `!name_is_alias_bound` as an
explicit conjunct, so `var x: Option[T] = src` denied `src` its whole reclaim
credit whenever `src` had no consuming match of its own. Nothing released either
slot afterwards.

The conjunct was correct for the code as it stood, and its own header said why:
these releases are a payload release plus a box dec, the bind took no retain, and
forgiving it alone leaves two slots decing one count. What was missing is the
other half of the pairing every container class already has.

## Measured

x86-64, `FERN_LEAKCHECK=1`, 100 rounds, `bin/fern -interp` and the native x86-64
backend agreeing on every exit code. The only variable is the bind.

| shape | native | before | after |
| --- | --- | --- | --- |
| `Option[i32[]]` alias, never read | 200/200/0 | **200/0 live 8000** | 200/200/0 |
| `Option[i32[]]` alias, alias matched | 200/200/0 | **200/0 live 8000** | 200/200/0 |
| `Option[string]` alias, never read | 100/100/0 | **300/0 live 7200** | 300/300/0 |
| `Option[Option[string]]` alias, never read | 200/200/0 | **400/0** | 400/400/0 |
| no alias at all | 200/200/0 | 200/200/0 | 200/200/0 |
| alias, SOURCE matched | 200/200/0 | 200/200/0 | 200/200/0 |

The last row is the one that located the gate: an alias is fine as long as the
source is itself matched, because that shape belongs to the consuming-match
family and its own alias gate (`opt_match_alias_escape_ok`) had already been
taught this. Only the unmatched quadrant refused.

## The shape of the fix, and the two ways it went wrong first

Both are worth recording, because **both balanced the census perfectly while
corrupting memory**:

| build | exit | census |
| --- | --- | --- |
| credit forgiven, no retain | **99** | `200/200 live 0` |
| credit + retain, deep release on both slots | **99** | `200/200 live 0` |
| correct | 36 | `200/200 live 0` |

The first is the failure #7687 predicted. The second is subtler and is the one
that cost the time: the alias slot now carried `"OPTARR:"`, so its BIND took
`emit_optarr_reclaim_store`, which frees a superseded payload and box and accepts
no `alias_inc` — the retain was dropped on the floor by the very path the credit
enabled. Gating that store (and its `OPTSTR:` sibling) on `!slot_nodeep` is what
routes an alias bind back through `emit_arr_store`, which emits the retain.

The correct pairing is the struct family's, unchanged:

- the bind RETAINS the box (a limb in `lower_stmt_var`'s ExprIdent ladder, gated
  on the three credit predicates so retain and credit are the same set);
- the alias takes the class credit qualified by `"NODEEP:"`, so its release is
  `emit_opt_box_only_free` — a null-guarded box dec, no tag read, no payload;
- the source keeps the one deep release, which runs exactly once whichever
  sweep is last.

## The vetting walker is `body_unsafe_for_match_borrow`, not `body_unsafe_for`

`opt_alias_bind_sites_of` vets the alias, and the plain walker reads a bare-ident
match scrutinee as an escape — which would refuse `match (x)`, the commonest use
of an alias, and leave that row leaking. That read is right for the PAYLOAD and
wrong for the BOX, so the pairing is the match-borrow walker plus
`!opt_body_binds_rc_payload`, exactly as `opt_match_alias_escape_ok` does one
family over. An alias that carries the payload out stays refused, and the leak
that refusal costs is pinned rather than balanced away
(`refuses_alias_carrying_payload_out`, frees 200 of 300).

## Gate

`TestSelfHostOptAliasBind{X86_64,WasmIR,IRArm64}` — twelve rows, each with a
second leg compiled under `FERN_SANITIZE=1`. The census is blind to both broken
builds above, so the exit code and the quarantining allocator are the
instruments; a `balance: true` assertion is necessary and nowhere near
sufficient here.

## What #7687 still has open

The enum half is DONE — measured clean on this build for all four of the issue's
alias rows (dead, alias-matched, source-matched, both-matched), closed by the
`rcenum_alias_bind_sites_of` work between the filing and now, not by this change.

The two rows still leaking are not an alias defect at all. `return`ing before a
consuming match strands the local, with no alias anywhere:

```fern
var src: Option[i32[]] = Some([i, i + 1]);
if (i >= 0) { return 5; }
match (src) { Some(b) => { return b.len(); }, None => { return 2; } }
```

`200/0 live 8000` against native's `200/200/0`. That is #7725's mechanism
widened: `optret_pending` is installed only around the candidate's OWN match
statement, so a return from anywhere earlier misses it. Filed separately; it
needs the pending set to cover the local's live range rather than one statement.
