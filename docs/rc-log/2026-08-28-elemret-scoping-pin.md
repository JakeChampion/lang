# The elemret floor stays with the promotion — scoped, pinned, declined

`tuple_mixed__elemret__payload_refused` is the last actionable leak row, and
this entry records why it does NOT get a per-cell fix like the last four, plus
the two instruments that now hold its ground.

## The scoping

The dup-at-extract port is the tuple wave's named retain-side precondition
(the 08-24 scenums-plan-routing entry sequences str/tuple LAST, behind it),
and every half-measure is measurably wrong:

- **Retain-only** (callee incs `return src.1`, TUPB stays 0): the element sits
  at rc 2 with one dec — a strictly worse leak — and it flips
  `tuple_mixed__elemret__box_tier_only` clean→leak, because that cell is clean
  today precisely by riding the uncounted element out to main's `is_arr`
  sweep. Same callee in both cells, so no caller-side scoping exists.
- **Grant-only** (TUPB forgives the element return): the sanitizer-confirmed
  UAF the 08-24 payload-tier entry already tried and reverted (exit 124 / 99).
- The refusal is spread over six sites (`rctuple_esc_expr`'s FieldAccess arm,
  the ret-ok scan, the TUPB tier, its alias collector, `TUPELEMOK:` issuance,
  the `ELB:` sibling), and the promotion doc forbids widening the enumeration
  those sites make up ("a leak fix that adds a new credit family deepens the
  thing being retired"). The 08-24 alias-param audit set the precedent: it
  declined the same kind of hand-widening, and those rows later went clean
  for free as a side effect of the struct wave.

## What moved instead

1. **An analysis-level pin** — `self_host_rcplan_diff_test.go`
   `tuple-elem-extract-bind`. It surfaced something the scoping had wrong:
   the BIND half already agrees (`var e = src.1` retains on both sides,
   anchored `aliasBindIncs 2:2=e`), which is why the elemret leak is one free
   short rather than a dangle. The self-host's actual analysis gap is
   OWNERSHIP of the extracted element — native marks `e` freeEligible with a
   last use, the self-host tracks neither (pinned per side) — plus the direct
   `return src.1` spelling's return-transfer inc, which no dumped table
   shows. When the port lands, the pins flip to anchored agreements.
2. **The coupling note on both matrix rows** (x86-64 + arm64): the two
   elemret cells move in OPPOSITE directions under either half-fix, so they
   are one coupled instrument, and any future attempt inherits that fact in
   the row it must edit.

## Where this leaves goal 2's gap list

Every remaining `leak` row is now either by-design on both sides
(`enum_scalar__callarg__stored_struct`), a notes-confirmed sound denial (the
two `alias_match` rows), or this floor, owned by the promotion's tuple wave
behind per-site plan verdicts and the struct release-protocol port. The
matrix's actionable-gap count is zero.
