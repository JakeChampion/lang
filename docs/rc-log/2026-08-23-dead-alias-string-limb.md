# Dead-alias cancellation, string limb — and the loop-rebind third release site

The #4402 opt 1 port's second limb: `var v = s` on a credited string source is
now cancelled like the array limb (#7455) — retain elided in the ladder, alias
sweep dec elided via `note_moved_elided`, both through the one predicate
`str_dead_alias_bind`. Strings have no precise-drop class, so the
`pd_filter_names` half is vacuous: only the inc/dec pair moves.

## Measured

- `alias-bind-string` flipped to anchored agreement (`aliasBindIncs` empty both
  sides, `freeEligible: s,v`); conditional, call-producer, and loop-scoped
  shapes anchored alongside it.
- `TestSelfHostContainerAliasBindX86_64` counts unchanged everywhere (a paired
  cancellation moves rc traffic, not allocs/frees); the new
  `string_alias_cancelled` row pins the cancelled path behind the
  `__rc_underflow_count` guard, which is what fires on an UNpaired elision.
- Source-reassigned and chain shapes stay retained on native but were never
  retained on the self-host — those `aliasBindIncs` divergences predate the
  cancellation (the rebind single-bind refusal and the credit collector's own
  chain refusal) and are now tabled per site in the rcplan diff.

## The trap: the pair has THREE release sites, not two

The elision covered the ladder retain and the exit sweep. A LOOP-scoped pair
(`while { var s = "hi" + "!"; var v = s; … }`) rebinds both slots each
iteration, and the alias slot's rebind store (`emit_str_reclaim_store` /
`emit_arr_store`'s dec-on-overwrite) is a third release the sweep-only elision
left armed: iteration 2 frees the prior box out of the source's slot at rc 1,
then the alias's cow guard sees `old != new` and frees it again.

- **The census is structurally blind to it**: the double free balances
  (`allocs=6 frees=6 live_bytes=0`, exit correct). Only the `FERN_SANITIZE`
  quarantine leg sees it — `use-after-free (touched a quarantined block)`,
  exit 124. Same instrument split as the #7358 era: only the dissenting
  instrument counts.
- **The array limb shipped with the same hole**: the identical loop shape
  trapped the sanitizer on the #7455 merge commit, pre-string-limb. Fixed here
  for both limbs at the shared point: a cancelled bind (the bind slot is
  `moved_elided` — only the cancellation marks a BIND slot) stores plain, no
  dec-on-overwrite, mirroring native's `emitVarReinitDropOld` skip at
  borrowed-alias sites (`ir.go:7964`).
- Permanent witness: `str__loop_local__alias_local` and
  `arr_i32__loop_local__alias_local` leak-matrix cells (clean/clean, both
  arches) — the sanitize leg every cell runs is the gate that catches this
  class.

## Next lead

The struct limb (`alias-bind-struct`, native `""` vs self-host `"4:2=v"`) is
the remaining pinned dead-alias divergence — it carries the `NODEEP:`
shallow/deep credit split, so the alias's elided dec is the box-only release,
and the same three-site audit (ladder, sweep, rebind store) applies before it
lands.
