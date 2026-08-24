# The alias_param family, audited — and why it stays with the promotion

An implementation attempt on the seven `*__alias_param` leak-matrix rows,
stopped on its own findings. No lowering change; what moved is the record.

## The taint chain, named end to end

Caller side: at `round(keep, i)`, `expr_unsafe_for`'s ExprCall arm asks
`param_is_borrowable` for round's param 0 and reads flag `'0'`, so the arg is
an escape and `keep` loses every kind's credit gate at once (the borrowable
registry is shared and kind-agnostic; the per-kind gates all bottom out in
the same lookup). Registry side: the flag is refused by the
`body_unsafe_for_match_borrow` conjunct in `borrowable_params_of` (and its
interproc twin), whose walker reaches the bare-ident StmtVar init with a
hardwired-empty alias list — the callee's `var x = src` alone flips the flag.
The `alias_ok` forgiveness machinery exists one call away and is simply never
fed there.

## Why the one-conjunct widening was declined

- **The promotion plan already owns these rows.** `SELFHOST-RC-PLAN-PROMOTION`
  classifies alias_param among the structural denials that "under an analysis
  are not cases at all", with acceptance "the 18 open rows go clean without
  any new per-row credit family" — and the origin-axis entry warns these rows
  "must move only with a key-aware credit, never with a naive widening". A
  hand-widened conjunct is more of the enumeration being retired.
- **Native's answer is not one answer.** For structs/tuples/arrays native
  sweeps the caller's keep (taint-propagation escape analysis: the callee's
  read-only alias reaches no sink). For STRINGS on x86 native REFUSES too —
  `paramCountedRetain` declines a param with a bare alias-bind occurrence,
  the string-arg taint keeps `keep` out of freeEligible, and the callee's
  alias inc goes deliberately unbalanced ("leak at worst, never
  use-after-free", string-only, single-word ABI only). A kind-agnostic
  widening would diverge from native in the string case in the direction
  native deliberately avoids.

## Matrix integrity findings (this change's actual diff)

- **The str origin cells' native column is vacuous.** `mkstr("kk")`
  const-folds (through const locals too — an ident seed does not help) and
  SSO keeps any short result inline, so native allocates NOTHING in those
  cells: `clean` there measures the absence of an allocation, not a sweep.
  Recorded on the kind's caveat and the two str alias_param row notes with
  native's real verdict for an allocating shape.
- **`tuple_mixed__*__alias_param` is mislabeled by its own axis**: the
  control (callee reads `src` directly, no alias) leaks identically —
  main's `(5, [6, 7])` keep is never released regardless of the callee's
  alias. The cell's leak is the caller-side tuple keep gap, not the
  alias_param mechanism. The rows stay pinned; the lead is that closing
  them needs the tuple keep release, not the borrowability widening.

## Measured (x86-64, constant-size confirmed at 100 vs 200 rounds)

| cell (fnscope alias_param) | native | self-host |
|---|---|---|
| str | 0/0/0 (vacuous) | 2/0/32 |
| str_arr | 1/1/0 | 3/1/32 |
| struct_arr_field | 2/2/0 | 2/0/88 |
| tuple_mixed | 2/2/0 | 2/0/80 (control leaks identically) |

Controls with the callee alias removed: str/str_arr/struct clean — the param
alias IS the taint for those three; tuple unchanged.

## Next lead

When the promotion reaches these rows, the free_eligible_of parity plus a
ported `paramCountedRetain`/escape summary replaces the borrowable flag —
the #7345 `countedSinkSource` pins in the rcplan diff gate are the same
class one edge over and leave with the same port.
