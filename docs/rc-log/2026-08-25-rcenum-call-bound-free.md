# A call-bound rc-enum earned the credit but never the free — killer-drops slice 13

`var v: E = mkv(i);` followed by a sole top-level consuming match reclaimed
NOTHING: **200 allocs / 0 frees** over 100 rounds against native's 200/200. The
byte-identical shape with the constructor written inline was flat.

Factoring a constructor out into a function is not a corner. The compiler's own
sources do it everywhere.

## Two passes decide this shape, and they disagreed

`collect_fresh_rcenum_names` resolves the init two ways — `fresh_rcpayload_enum_init`
for a direct or qualified ctor, and `rcenum_call_init_owner` for a call to a fn in
the `"RCE:"` registry of whole-program-proven fresh rc-enum ctors (slice 4). On a
sole top-level match it grants `"RCENUM:"` alone, which is the credit that
**suppresses the exit sweep** — because the consuming-match free is supposed to
release the value instead.

`consumed_rcpayload_enum_frees`, the pass that places that free, resolved with
`fresh_rcpayload_enum_init` alone.

So the call bind lost its sweep to a free that was never emitted. That is the
worst of the two orders: had the free fired without the credit, the value would
merely have been released twice as loudly. Here it was released not at all.

The gap is visible in the emitted asm — `round` gets one `__fern_arr_dec` pair
for the call form against two for the inline one.

## The registry proof is stronger than the test it now joins

Admitting the call form is not a relaxation. `body_has_nonqualifying_rcenum_return`
requires EVERY return of a registered fn to satisfy `fresh_rcpayload_enum_init`
against the strict (empty) fresh-string set, and then
`rcenum_ctor_payload_strings_fresh` on top of that. A registered call hands over
exactly the sole-owned chain an inline ctor does, with one extra gate.

The rebind gate had the same split: `consumed_rcpayload_enum_frees` checked
`all_assigns_fresh_rcenum` against `sig_reg_empty()` while the credit side passed
the real registry, so `v = mkv(i + 3)` failed for the same reason the init did.
Both now take the registry.

This is the rc-payload user-enum analogue of #6360, which made the identical
admission for scalar Option/Result (`self_host_call_bound_enum_reclaim_test.go`).
That fix's own note observed that "the user-enum sibling has admitted call inits
since #4355 slice 5" — true of the credit side, and the half that emits the free
is what had not.

## Results

| shape | before | after | native |
|---|---|---|---|
| call-bound, sole top-level match | 200/0 | **200/200** | 200/200 |
| call-bound, rebound | 400/200 | **400/400** | 400/400 |
| call-bound inside a loop block | 400/200 | **400/400** | 400/400 |
| call-bound, STRING payload | 300/0 | **300/300** | 300/300 |
| call-bound, STRUCT payload | 300/0 | **300/300** | 300/300 |
| payload read back after three fresh arrays | value 9 | value **9** | 9 |
| inline-ctor sole match (control) | 200/200 | 200/200 | 200/200 |

The churn row is the one that carries the soundness. Counts and
`__rc_underflow_count()` are blind to a use-after-READ — `2026-08-25-field-reclaim-shared-box.md`
records shipping one that passed both AND `FERN_SANITIZE=1` — so it asks for the
VALUE with three fresh arrays allocated after the match, against native's answer.
All three backends agree.

Every one of the 75 rc probes in the scratchpad set was run through the before and
after compilers: the only rows that moved are the ones above, and every exit code
is unchanged.

## Two refusals held, and they are what makes the widening honest

- **A producer that returns its param** (`function passthru(e: E): E { return e; }`)
  never enters the registry, so the call bind stays unresolved: 200/0 before and
  after. It leaks, which is the safe direction. If that row ever reports a free,
  the admission has reached a producer handing back a box it does not own.
- **A guarded arm mixed with a moved payload** stays refused by the `guarded_move`
  gate: 350/150 before and after. A guard could divert execution to an arm that did
  not move the payload, which would turn the moved-set skip into a per-call leak —
  or, run the other way, free a payload the escapee holds.

## What this exposed rather than caused

This also moves the FIRST of the two gaps `2026-08-25-enum-match-scrutinee-borrow.md`
left open. That entry is written-once, per this directory's README, so the
correction lives here: its `sole_match_binds_payload_out_still_refused` row, pinned
at 300/100 and described there as taking a branch this could not reach, is now
300/200 and renamed `sole_match_binds_payload_out_box_freed`. The box is reclaimed;
the payload is not.

That shortfall is NOT this admission's. `match_moved_rc_payloads` skips the moved
field's dec on the theory that the arm binding took over the box's reference,
while `keep = xs` RETAINS: one claim is never balanced. The identical shape bound
from an INLINE ctor measures 300/200 both before and after this slice, so the
behaviour predates it — the call form has simply been brought level with the
inline one, which is the whole point.

Closing it means teaching the moved set the difference between a move and a
counted share. That is its own slice, and it is the next one.

Pinned across x86 / arm64 / wasm by
`internal/e2eselfhost/self_host_rcenum_call_bound_free_test.go`, with 99 reserved
for an over-release.
