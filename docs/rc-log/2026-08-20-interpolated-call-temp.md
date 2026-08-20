# An f-string interpolating a call stranded that call's box

`f"{w(pre)}"` leaked one box + data per evaluation, while the same call written
as an explicit concat operand leaked nothing. 400 rounds of the churn harness, a
pair of compilers from the same commit:

| shape | x86-64 | arm64 | wasm |
| --- | --- | --- | --- |
| `f"{w(pre)}"` | 54400 → **0** | 54400 → **0** | 48000 → **0** |
| `f"{w(pre)}-{w(pre)}"` | 108800 → **0** | 108800 → **0** | 96000 → **0** |
| `w(pre).to_string()` | 54400 → **0** | 54400 → **0** | 48000 → **0** |
| `f"{pre}"` | 0 | 0 | 0 |
| `f"n={n}"` | 0 | 0 | 0 |
| `pre + "-" + w(pre)` | 0 | 0 | 0 |

## Cause

`parser.fern` desugars every interpolant to `(<expr>).to_string()`. On a STRING
receiver that is the receiver-identity fast-path, which
`str_local_binding_is_fresh` excludes by design: the result IS the receiver, so
freeing it would release a box the source still owns.

That reasoning holds for a NAMED receiver and **inverts for an anonymous one**.
When the receiver is a call the whole-program proof says returns a fresh
sole-owned box, the identity result is that box, this frame is the only thing
holding it, and not freeing it is the leak.

## How it was located

By bisecting the shape, not by reading the desugar. The f-string case leaked and
the explicit-concat case did not, which looked like an f-string machinery bug
until `w(pre).to_string()` — written by hand, no f-string involved — reproduced
the number exactly. That is what pointed at the `.to_string()` rule; the desugar
then confirmed it in one line.

The two-interpolant case leaking exactly twice as much said the credit had to
fire per interpolant rather than per f-string, before any code was written.

## Fix

`tostring_on_fresh_ret` credits `<fresh-ret call>.to_string()` at the binding
site, and `is_fresh_str_temp` gains the matching arm for the concat-operand
position. The desugar is untouched: it mirrors the Go checker's FString lowering,
and changing it would diverge the two compilers to fix something that is a
reclaim question either way.

## The risk this carries

Widening `is_fresh_str_temp` is the move #6590 records as having broken two
over-release contracts. Its ~20 callers drive the accumulator and concat-temp
analyses, and this is exactly the predicate the `.to_string()` scalar work
deliberately refused to widen for that reason.

The difference is what the arm is gated on. The scalar case needed a receiver
TYPE the state-free classifier could not see; this one is gated on
`is_fresh_str_temp` itself — already the predicate every caller trusts — so the
new arm admits a strict subset of what the callers already treat as freeable.
Verified rather than argued: the accumulator, concat-temp, fresh-string and
to_string suites all run green, and three controls (`f"{pre}"`, `f"n={n}"`, the
explicit concat) sit in the new suite specifically so a future widening that
swallowed one of them fails as an over-release instead of changing class in
silence.

## Witnessed

- **The release**: the three positive cases fail with 98 on the parent, on the
  x86-64 and wasm legs; the three controls pass there.
- **The identity gate**: a user `to_string` returning a field ALIAS is refused,
  both on a struct local and on a fresh-ret struct TEMP — the receiver being
  fresh says nothing about the result once a user method is in the way. `mk`
  returns a struct so it is not in the string fresh-ret registry, and the case
  exists to prove that rather than assume it.
- **Liveness** across all three classes at once: a named-receiver identity stays
  uncredited and its source survives, a fresh-temp identity is credited and still
  reads, and an f-string mixing a call with a live local is correct — all behind
  decoy allocations.
