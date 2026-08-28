# The alias forgiveness reaching the enum's second escape scan

`enum_rc_payload__fnscope__alias_match` flips `clean leak` → `clean clean`. One
of the two rows where the self-host leaked and native did not; the Option twin
is the last one and is a separate fix.

## The recorded diagnosis was wrong

#7687 landed the confined-alias forgiveness for rc-enums and stated why this row
did not move with it:

> `enum_rc_payload__fnscope__alias_match` matches BOTH the alias and the source,
> so the source takes the match-consumed branch and is refused there by
> `match_arm_binds_rc_payload` — a different gate, and widening it is a
> payload-move question this change does not touch.

Bisecting the cell before touching anything says otherwise:

| shape | self-host |
| --- | --- |
| source + one match on it | clean |
| alias + match on the ALIAS | clean (what #7687 fixed) |
| **alias + match on the SOURCE** | **200/0 leak** |
| the full cell (both matched) | 200/0 leak |
| **two matches on the source, NO alias** | **clean** |

The last row is the one that settles it: two matches on the source are fine, so
"the source takes the match-consumed branch and is refused there" is not the
cause. `match_arm_binds_rc_payload` is not implicated at all — it was already
widened for borrow-only binds, which is what the cell's arms are. The minimal
failing shape is a *dead alias bind* plus a match on the source.

## The actual cause: a second escape scan

The rc-enum credit has two paths, and they consult different escape scans:

- no consuming match → the exit-sweep credit, gated by
  `body_unsafe_for_enumfield_alias`, which #7687 taught the forgiveness;
- a consuming match → `consumed_rcpayload_enum_frees`, gated by
  `name_escapes_outside_stmt_enumfield`, which it did not.

So the alias denied the source only on the path #7687 did not reach. This is the
standing "a class consults more than one escape scan" trap, and the third time
it has bitten in this area — the same shape cost the tuple counted-store wave a
routing edit earlier the same day.

The fix gives the second scan the same forgiveness, keyed by the same proof
(`rcenum_alias_bind_sites_of`), so the two cannot come apart: a CONFINED alias
takes no release of its own, and skipping its bind leaves the source the sole
releaser. Only the bind statement is skipped, and its whole init *is* the bare
ident, so nothing else in it goes unscanned.

## Guards

Because the proof is shared, its second half — `!enum_body_binds_rc_payload`,
the half #7687 found the hard way — still refuses the shapes that would
over-release. Measured, all matching both oracles with no underflow:

| shape | verdict |
| --- | --- |
| alias hands the payload out (`Full(xs) => out = xs`) | refused, leaks |
| alias returned from the frame | refused, leaks |
| source rebound while the alias is live | refused, partial |
| the Option twin | unchanged (its gate is `name_escapes_outside_stmt`) |

The payload-out shape is the one that matters: #7687 recorded it firing
`__rc_underflow` when admitted, with a balanced census either way, so it is a
free-safety guard and not a leak-accounting one.

## What is left

`opt_arr__fnscope__alias_match` is now the ONLY row in the 134-cell matrix where
the self-host leaks and native is clean. It is the same defect one family over —
`consumed_rcpayload_option_frees` calls plain `name_escapes_outside_stmt`, a
THIRD scan without the forgiveness — but closing it needs an Option-flavoured
confinement proof, and the payload-out half of that proof carries the same
over-release hazard. That is the next increment, not a widening of this one.
