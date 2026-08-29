# The last self-host-only leak was an alias bind read as an escape

`opt_arr__fnscope__alias_match` flips **leak → clean** on both architectures.
With it the leak matrix has **no self-host-only leak left on either arch**.

## What it was

`consumed_rcpayload_option_frees` is the per-block consuming-match free for
Option payloads. Its admission reads

    if (!arms_use && dead_after && !escapes && !binds_esc)

with `escapes = name_escapes_outside_stmt(body, v.name, match_idx)`. An alias
bind `var x = src;` is a mention of `src` in a statement outside the match, so
`escapes` was true and the free was declined.

That decline is conservative-correct in isolation. The bug is that **nothing
else covers the shape**: `Option[<flat scalar array>]` has exactly two credits
and an aliased, matched, non-reassigned local falls between them —
`collect_fresh_optarr_names` requires the name to be REASSIGNED (#6127), and
`collect_unmatched_optarr_names` requires it to be NON-match-consumed (#6360).
The `OPTSTRUCT` and `OPTARRARR` siblings each carry a block-level
consuming-match analysis; the flat-array Option does not. The enum family has
had the alias-aware reading since #6606, which is exactly why the same shape
over an enum was already clean.

## Why the fix cannot double-free

The alias's init is an IDENT, never a fresh `Some(..)`, so no candidate scan
ever admits the alias itself. The source is the only name that can hold the
credit, and it frees the one box once. Two further proofs carry the rest:

- the alias is not read after the match that frees the box
  (`name_used_after`), so no surviving alias reaches freed memory;
- the alias is cleared by `body_unsafe_for_match_borrow`, not the coarse
  `body_unsafe_for`. An alias consumed by its own `match (x)` reads the box as
  a BORROW — that match frees nothing, because the alias is not a candidate —
  and the coarse walker counts it as an escape, which refused the very shape
  this exists for. Everything else it refuses still refuses: a second alias, a
  return, a call argument.

## Measurement

Bisected with `FERN_LEAKCHECK=1` (allocs / frees / live_bytes), before → after:

| shape | before | after |
|---|---|---|
| alias, both matched (the cell) | 200 / 0 / 8000 | 200 / 200 / 0 |
| alias, source matched | 200 / 0 / 8000 | 200 / 200 / 0 |
| alias declared, never used | 200 / 0 / 8000 | 200 / 200 / 0 |
| alias RETURNED (must stay refused) | leaks | leaks — unchanged |
| two independent Options | 400 / 400 / 0 | unchanged |
| only the ALIAS matched (residual) | 200 / 0 / 8000 | unchanged |

Leak matrix: the target row is the ONLY row that moved, on either
architecture; 0 failures, 0 errors. `FERN_RC_UNDERFLOW_TRAP=1` on the cell
exits clean, and the fixture's escaping case still leaks 8000 with no
underflow — the guard still guards.

## Residual

`var x = src;` where only the ALIAS is matched and the source is not still
leaks. That shape has no consuming match on the source at all, so it belongs
to the UNMATCHED collector, whose `opt_unmatched_esc_ok` rejects an aliased
name outright (`!name_is_alias_bound`). Patching that predicate was tried and
does nothing for the matched cases — it guards the other quadrant — so the
residual is a separate, smaller piece of work against a different gate.

## Instrument note

`FERN_LEAKCHECK=1` and `FERN_RC_TRACE=1` are EMIT-TIME ports: set them when
COMPILING. Set only at run time they print nothing at all, which reads exactly
like a clean run. `docs/TEST-GATES.md` says so; it is an easy line to skim.
