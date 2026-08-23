# Dead-alias cancellation, struct limb — and the full-moved-set gate

The #4402 opt 1 port's third limb: `var v = p` on a credited struct source is
cancelled through `struct_dead_alias_bind`, same two-consultation shape as the
array and string limbs. Under duplication the alias holds only the box-only
`NODEEP:` release, so that box dec is the whole of what the cancellation
elides — no credit surgery (the move path's release-role transfer stays its
own), and the source keeps its one deep field walk at the exit sweep. The
loop-rebind third release site is covered by the string limb's shared
plain-store guard, which precedes every dec-carrying store route.

## Measured

- `alias-bind-struct` flipped to anchored agreement; loop-scoped struct shape
  anchored; returned-alias exclusion anchored with its retain (native
  precise-drops the source at last use, the self-host sweeps — the placement
  class, tabled).
- New witnesses: `struct_arr_field__loop_local__alias_local` (the rebind
  double-free class, sanitize leg) and
  `struct_arr_field__fnscope__alias_reuse` — a cancelled pair with a later
  same-type construction while the alias is live, pinning that the reuse
  donor gate refuses the source (its bare-ident alias mention is an escape in
  `walk_expr_escapes`); at rc 1 a donated box would be freed under the live
  alias, and only the sanitize leg would see it.
- `struct_alias_cancelled` container row behind the underflow guard.

## The gate the review added: the pure half now reads the FULL moved set

`dead_alias_of` gated moves through the emitting predicates' top-level-only
sets (`moved_locals_toplevel_of` / `moves_local_at`), but native's
`movedLocals` exclusion includes the #5879 loop-body construction moves. A
loop-body-moved source (`while { var p = P{…}; var v = p; …; vals =
vals.append(p); }`) therefore cancelled on the self-host where native
refuses. Balanced in practice — today such a source is also uncredited (the
append escape), so no retain fires either way — but the gate premise must not
depend on the credit model staying that narrow: `dead_alias_of` now takes the
full `rc_ml_compute` names and refuses pairs on them, for all three limbs.
The shape is tabled (`dead-alias-struct-loop-body-moved-source-excluded`),
with the credit-refusal divergences (`freeEligible`, `aliasBindIncs`,
`lastUses`, `nestedDrops`) pinned per table.

## Known coverage gap (tabled, not a hazard)

A call-producer struct source (`var p: P = mk();`) is not admitted by the
pure half — struct evidence is struct-LITERAL-only, since `dead_alias_of` has
no structs registry to resolve an annotation, while arrays and strings have
syntactic annotation routes. Native cancels; the self-host keeps the (net
zero) pair. Pinned as `dead-alias-struct-call-producer`. The lead if it ever
matters: thread the fresh-struct-ret registry (which the slot-fact side
already consults) into the evidence walker.

## Next lead

Tuple pairs are the remaining uncancelled kind with alias machinery (the
`TUP:`/`TUPRC:` classes); no live-source tuple pin exists yet, so first
measure native's verdict on that shape before porting.
