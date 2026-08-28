# Dup-at-extract for tuple element returns

Closes `tuple_mixed__elemret__payload_refused`, the floor the 08-28 scoping
entry handed to the promotion's tuple wave. Both halves land together because
each alone is measurably wrong, in opposite directions — the two coupled
matrix rows are the instrument that says so.

## One predicate, two entry points

`tuple_elem_ret_dup(e, name, ttype)`: `e` is exactly `name.<i>` and element
`<i>` is an rc ARRAY. The return lowering's `ret_is_tuple_elem_dup`
emits `retain_tos("ret-tuple-elem")` for exactly that shape, and the rc-tuple
escape scans (`rctuple_esc_stmt_alias`, `..._payload_escapes_alias{,_ret_ok}`,
under a new `ret_dup_ok` conjunct) forgive exactly that shape. So "the escape
stops being an escape" is true by construction, never because two conditions
happen to line up — the 08-22 struct-producer rule, applied to a case where
getting it wrong is a use-after-free rather than a null result.

String, struct and nested-tuple elements are refused on BOTH sides: their
retain protocol is a separate port.

## Who grants, who refuses

| site | verdict | why |
|---|---|---|
| `tuple_payload_borrow_flags` (TUPB) | `ret_dup_ok = true` | `pty` is the param's DECLARED type, and the slot's `tuple_elems` derives from that same declaration — scan and emitter provably see one fact |
| `TUPELEMOK:` / `TUPRC:` / `TUPRCS:` issuance | `true` | annotation-derived `ttype`, same argument |
| `rctuple_param_alias_bind_sites` (the alias vet) | **`false`** | an unannotated alias slot may carry no `tuple_elems`, so the emitter would NOT retain — forgiving it there is precisely the v1 UAF |
| `rctuple_esc_expr` (the expression walk) | unchanged | context-free, used in non-return positions; the admission is a statement-level fact |
| `arrenum_elem_borrow_flags` (`ELB:`) | unchanged | its element returns have no retain yet; admitting them needs their own retain plus the same shared-predicate treatment |

## The analysis half

`rc_fe_rhs_tainted`'s `ExprFieldAccess` arm now mirrors native `rhsTainted`'s
tuple-source case: an element read out of a tuple-typed local or param is a
COUNTED alias, so the destination OWNS its reference. Map elements stay
tainted (their slot drop deep-frees the value column). The struct-source half
of native's arm is deliberately not ported — the conservative taint is the
documented cut.

That flips the two divergences the 08-28 entry planted in the rc-plan diff
gate (`tuple-elem-extract-bind`) to anchored agreements: `freeEligible: e`
and `lastUses: e=1` now match native, alongside the bind retain that always
agreed.

## Measured

| shape | before | after |
|---|---|---|
| `tuple_mixed__elemret__payload_refused` | 2 allocs / 1 free / 40 live | **clean** |
| `tuple_mixed__elemret__box_tier_only` | clean | clean, new arithmetic |
| direct `return src.1` in a 100-round loop | one free short per round | balanced, live 0 |
| self-extract (`pick(t)` where the caller owns `t`) | box leak | balanced, live 0 |
| conditional element return (live arm) | leak | balanced, live 0 |

Element rc across a round, post-change: construction 1 → return retain 2 →
the container's deep free 1 → the final owner's sweep 0.

## Non-vacuity, both directions

The coupling IS the instrument, and it was exercised as one:

- **Retain removed, grant kept**: the direct probe exits **99** — the granted
  deep free decs a count nothing added.
- **Grant removed, retain kept**: `tuple_mixed__elemret__box_tier_only` flips
  **clean → leak** — the element arrives counted with nobody to release the
  container's claim.

Each knockout flips a DIFFERENT instrument, which is what makes the pair a
proof rather than a coincidence.

## Pins that moved, and their reading

- `handout_elem_keeps_refused` (alias-vet row): frees 101 → **100**. The
  annotated alias's `return x.1` is now retained, so the element leaks WITH
  `keep`'s box instead of being freed under `keep`'s live reference — one
  leak deeper, strictly safer, and TUPB staying refused is what the row pins.
- `bind_spelling_stays_refused` (new): 200/0/8000, pinned at frees 0. The
  bind spelling (`var e = src.1; return e`) is deliberately outside this
  port — the scans' `StmtVar` arm is untouched.

## Deferred, recorded here so the enumeration stays honest

The bind-spelling TUPB admission (needs an escape vet on `e`), the `ELB:`
array-of-boxes analogue, and the struct-source half of the fe arm.
