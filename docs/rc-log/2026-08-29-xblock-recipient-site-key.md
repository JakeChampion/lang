# The cross-block reuse pairing keyed its recipient by name

`xblock_pending` carries the cross-block FBIP pairings: a `var d = T{…}` at a
block's top level, dead by statement k, whose box a `var c = T{…}` at the top of
a later if-arm reuses in place. The rows were `"<recipient name>|<donor>"`, and
`xblock_donor_for` returned the donor of the **first** row whose pre-`|` half
matched.

Two things make one spelling able to mint two rows:

- `xblock_pairings_for` deliberately restarts the else arm from the same
  `consumed` set (#4402 opt 3), so both arms of one `if` are scanned
  independently; and
- an if-arm's recipient is an ordinary block-scoped `var`, so sibling arms
  naturally use the same name.

Under the first-match read, one arm's recipient then resolved the other arm's
donor — and the emitters do not merely read it. `emit_cross_struct_reuse`
overwrites the donor's box in place and runs `emit_reuse_recip_prior_release`
against it, so a mis-resolved donor is a destructive write to the wrong box.

The recipient key is now the binding SITE. `xblock_scan_body` has `rv.line` /
`rv.col` at both producers, and the consumer in `lower_block` was already
computing `reclaim_site_key_of(rv.name, rv.line, rv.col)` two lines below the
lookup to pass to the emitters — so the key it needed was in scope the whole
time.

## Unwitnessed, and why the probe came back clean

Sibling arms each constructing a `P` with a same-named recipient, over 200
rounds with an underflow guard, measured **0 on both sides** of the change.

That matches what the mechanism predicts rather than contradicting it. The
then-arm's `xblock_without` strips its row before the else arm is lowered
(`lower_block`'s `s2` threads into the else arm), so the else recipient usually
finds nothing and allocates fresh — a perf loss, not a corruption. The
destructive case needs the removal NOT to run, which happens when the then-arm's
construction fell through to `lower_stmt` instead of the reuse emitter.

Recording it as a first-match hazard closed rather than a bug fixed. The reason
to close it anyway is that this family is under an **observational-identity
contract** — `reuse_layer_disabled` says reuse-on and reuse-off must produce
identical behaviour, asserted by `self_host_reuse_differential_test.go` — so any
behavioural difference from a name collision here is a contract violation, not
a missed optimisation.

## The donor half is still a name, deliberately

`emit_cross_struct_reuse`, `emit_cross_tuple_reuse`, `emit_enum_donor_reuse` and
`emit_enum_cross_reuse` all resolve the donor with `slot_of(donor_name)`, which
answers with the innermost binding of that spelling. `slot_of_site` now exists
to convert them, but the four emitters are shared with the SAME-block reuse
path, whose producers hand them names from a different scan — so converting the
donor means converting five producers and four emitters together, or carrying
two keys for one value, which is the shape #7358's `"RCENUM:"` note warns
against. It is its own slice.
