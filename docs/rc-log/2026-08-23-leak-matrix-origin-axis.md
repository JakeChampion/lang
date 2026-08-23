# The leak matrix grows the origin axis, and the site-keying holds

Leak-matrix v2 — the axis #7253's probe-audit demanded: every v1 cell bound
`x` from a fresh construction (a local-var origin), and the day's defects all
sat on the origin axis. The generator now also binds the readable kinds from
an aliased LOCAL (source read again after the alias) and from a PARAMETER
main builds once, keeps live across every call, and reads after the loop —
the two conditions the thread's probe rules require — plus hand cells for the
for-in element binder, field reads from a local and a param, and an
alias-then-consuming-match on the enum and Option kinds. 90 → 115 cells,
~21 s.

## The headline: zero crashes, zero underflows, every exit matching native

Across 25 origin cells stressing exactly the shapes that produced yesterday's
SIGSEGVs and exit-99s, nothing over-releases. The site-keying migration
(#7272/#7349/#7356/#7358) means an aliasing binding is REFUSED rather than
handed a sibling's credit — every defect on this axis is now in the leak
direction, which is the direction the thread's ordering rule paid for.

## Thirteen new leak rows, in three classes

- **`str_arr__*__alias_local` (per-round).** The `string[]` sibling of the
  #7282 alias-credit family. `str` / `struct` / `tuple` local-alias cells
  measure CLEAN because those credits landed today (`rc(#7282)` ×3, other
  sessions); `string[]` has not had its turn. The matrix caught the family's
  missing member the day the family was being filled in.
- **`*__alias_param` (constant, not per-round).** Passing `keep` as a call
  arg escape-taints it, so main's own local loses its exit sweep — a
  constant-size divergence (native sweeps it). `arr_i32` is immune because
  the array sweep is driven by the `is_arr` slot flag, not a deniable credit
  — the thread's "reference implementation is silent on the failure mode"
  observation, measured.
- **The denial pins.** The for-in binder (no collector credits a non-StmtVar
  binding; #7292/#7356 family), and alias-then-match on enum/Option (credit
  denied while the source lives — sound, both boxes leak). These are the
  latent class the thread warns about: rows that must move only with a key- 
  aware credit, never with a naive widening — the underflow guard on every
  cell is what fires if someone tries.

`field_read_arr` both flavors measure clean — the #7343 `field_read_admitted`
row's behaviour, from the matrix's angle.

## Regenerating

`FERN_LEAK_MATRIX_DUMP=1` as before; the 115 verdicts in the testdata file
come from that run.
