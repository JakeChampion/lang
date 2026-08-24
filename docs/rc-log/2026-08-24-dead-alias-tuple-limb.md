# Dead-alias cancellation, tuple limb — the last alias-credit kind

The #4402 opt 1 port's fourth limb: `var v = t` on a credited tuple source is
cancelled through `tuple_dead_alias_bind` (slot gate: `slot_is_tuple_box` AND
a `TUP:`/`TUPRC:`/`TUPRCS:` credit). Under duplication the alias holds only
the shallow `TUP:` box dec — the deep `TUPRCS:` release stays with the source
— so that box dec is the whole of what the cancellation elides. No credit
surgery; the move path's `TUPRCS:` transfer stays its own and takes
precedence. The pure half's tuple evidence is a paren-prefixed annotation
(fn-types excluded by the `=>` test) or an `ExprTuple` init at the source's
declaration. With this limb, every kind with alias-credit machinery (array,
string, struct, tuple) cancels; enums/options have no alias-bind credit to
cancel (the `alias_match` cells stay the denial rows they were).

## Measured

- Probe first: native cancels the live-source tuple pair (`aliasBindIncs`
  empty, `freeEligible: t,v`) where the self-host retained `3:2=v` — then the
  port, and `alias-bind-tuple` anchored. Returned-alias exclusion anchored
  with its retain (placement-class `preciseDrops` divergence pinned);
  loop-scoped shape anchored, with `tuple_mixed__loop_local__alias_local` as
  its sanitize-leg witness and a `tuple_alias_cancelled` container row behind
  the underflow guard.
- **The limb fixes a baseline leak**: the unannotated alias
  (`var t: (i32, i32[]) = (i, [i, i+1]); var v = t;`) retained at the bind
  but the unannotated slot never earned the balancing sweep dec — 4000 bytes
  per 100 rounds. The cancellation elides both halves of the unbalanced pair;
  clean now, matching native.
- The reuse-donor hazard (cancelled source at rc 1 recycled under the live
  alias) is refused by `walk_expr_escapes` on the bare-ident alias mention —
  nine adversarial probes exit-matched native with zero underflows and zero
  sanitizer findings.

## Tabled boundary

A call-producer tuple source (`var t = mk()`): native's `freeEligible` admits
any owned-args call and cancels; the self-host's tuple credits are
literal-init (or direct-scalar-literal-ret) only, so the source is uncredited
— no retain, no cancellation, no release. The rcplan tables AGREE on both
sides here (`free_eligible_of` is the ported analysis; the credit table is
what diverges and is not dumped), so no rcplan pin is possible — the
`tuple_mixed__callprod__alias_local` leak-matrix cell (clean | leak) is the
one instrument documenting it.

## Leads left on the table (pre-existing, found by the review)

- Tuple alias vetting has no element-aware gate: `var e = v.1` through an
  alias is invisible to `rctuple_payload_escapes_alias` (source-name-only)
  where the direct `var e = t.1` refuses the credit — the same class
  `strarr_alias_bind_sites_of` was given its element-aware gate for. No
  reachable over-release today (probed); worth mirroring the strarr fix.
- An rc-tuple returned behind a conditional (`if … { return (0, [0]); }
  return t;`) leaks on the self-host (200 boxes per 100 rounds) where native
  is clean — the keep-sweep interaction for conditionally-returned rc-tuples,
  independent of aliasing.
