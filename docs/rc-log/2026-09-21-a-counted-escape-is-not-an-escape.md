# A counted escape is not an escape

#8003, filed as "tcp_serve leaks ~5.6 KB per request". It is not a tcp bug: the
serve loop's request drain writes

```fern
match (tcp_recv_deadline(fd, 4096, remaining_ms)) {
    Some(c) => { chunk = c; },
    None    => { timed_out = true; },
}
```

and both fresh-call-scrutinee reclaims refused it, so the recv buffer the callee
had just allocated was abandoned — once per request, forever.

Same program as the report, x86-64-linux native, `VmRSS` sampled while curl
drives it:

| requests | before | after |
| --- | --- | --- |
| 0 | 72 KB | 76 KB |
| 300 | 1708 KB | 212 KB |
| 600 | 3332 KB | 336 KB |
| 900 | 4952 KB | 456 KB |
| 1200 | 6576 KB | 580 KB |

5.42 KB/request to 0.42 KB/request. The 92% is one row of the trace census,
`__alloc_u8 from __fern_tcp_recv`, and it disappears.

## Cause

Both reclaims gated on `bindingConfinedToArm`: every mention of the binding has
to be a read THROUGH the value. `chunk = c` is a store, so the walk refused it,
and with it the whole match.

Confinement is sufficient but not necessary. The question the reclaims need is
whether any reference OUTLIVES the arm uncounted — and `chunk = c` incs what it
reads (`needsRcIncOnAlias`, the *ast.Ident-target case of `assign`). The payload
stands at rc 2 when the arm ends, the release takes it to the 1 `chunk` owns,
and `chunk`'s own drop frees it.

Hence `bindingReleasableInArm`: `bindingConfinedToArm`'s walk plus one excuse,
an assignment to a local whose lowering provably retains. The guards mirror the
emitting site one for one — a move site and an owned-container read each skip
the inc, and a skipped inc is exactly the uncounted escape that must stay
refused. A `return`, a re-wrap into a constructor, and a callee that hands the
argument back are all still refused.

`bindingConfinedToArm` keeps the strict reading, because the borrow analysis
also behind it (`computeFreeEligible`'s view-alias walk) is asking whether a
local is a pure view of another value, and a counted copy is what disqualifies
one.

## Both reclaims, and why the box half needed a second fix

The report's reduction returns `Option[u8[]]`, which is PAIR FORM — no box, so
what leaks is the payload array and the gate is `reclaimablePairFormPayload`. A
two-payload variant (`Got(u8[], i32)`) is too wide for that ABI and goes through
a heap box, where the gate is `reclaimableMatchScrutinee` and the leak is the
box AND the array.

Widening the predicate fixed the pair-form half and did nothing for the boxed
one, because `reclaimableMatchScrutinee` runs BEFORE the arm's bindings are in
scope: `b.exprType` on the binding ident reads nil there, so the type half of
`needsRcIncOnAlias` answered false and the excuse never fired. The arm's own
`BindingTypes` entry has the answer, so the type half is split out
(`rcIncOnAliasType`) and passed the type the caller already holds. That is the
trap in this area — a predicate that reads correct and is being asked before its
operand exists.

Census on the reduction, 200 rounds, x86-64 and arm64 identical:

| | before | after |
| --- | --- | --- |
| pair form (`Option[u8[]]`) | 1400 / 1212, 21,056 B | 1400 / 1400, 0 |
| boxed (`Got(u8[], i32)`) | 1588 / 1212, 27,072 B | 1588 / 1588, 0 |

Allocation count is unchanged, which is the point of taking this over the hoist
the issue also priced: rewriting the scrutinee onto a temp first (self-host's
`hoist_call_scrutinees`) reaches the same reclaim at one extra allocation per
round — 1400 becomes 1600 on the same reduction.

## The model this rests on was already in the tree

`docs/rc-log/2026-08-30-iter-adapter-pair-form-payload.md` fixed the sibling
shape `cur = t.1` and states the accounting outright: "the tuple owns the fresh
iterator at rc 1, `cur = t.1` retains it to 2, and the deep drop takes it to 1
held by cur. A SHALLOW free would leave it at 2 and still leak." That shape only
ever reclaimed because `t.1` is a FIELD ACCESS, which the confinement walk
already excused. A bare ident in the same position was not, which is the whole
of the difference.

## Gates

`internal/e2e/rc_escaping_match_payload_test.go` runs the pair-form shape, the
boxed shape and the bound-scrutinee control on x86-64, arm64 and wasm, asserting
`allocs == frees` with `__rc_underflow_count() == 0`. Both natives also run
clean under `-sanitize`.

`TestPairFormPayloadKeptWhenBindingEscapes` asserted the old refusal and called
the release a use-after-free. It is now
`TestPairFormPayloadReleasedWhenEscapeIsCounted`, asserting the release: its own
program, run rather than read, is balanced at 5/5 and reads `kept[0..2]` back as
1/2/3 after the loop.

`TestMatchBindingRebindOverRetainsX86_64` pinned this defect DELIBERATELY —
`docs/rc-log/2026-08-30-match-binding-rebind-overretain.md`, rc 2 and 3 unpaired
where 1 and 0 are correct — and now reads 1 / 0. Its own diagnosis was right
("the arm-end release is ABSENT when the binding is assigned out, while the
alias-inc is still emitted"); the repair it sketched, suppressing the inc, was
the wrong half, because the inc is what makes the destination an owner. It is
now `TestMatchBindingRebindOwnsOnceX86_64`, in
`internal/e2e/match_binding_rebind_test.go`.

That entry also expected the conformance leak census to improve with the fix.
**It does not.** Regenerated over all 518 fixtures, every row is unchanged — so
the corpus does not exercise an assigned-out match binding anywhere, and the
census is as blind to this shape as it is to the serve loop. Two gaps in one
gate, both worth a fixture.

## What it does not reach

The residual 0.42 KB/request in the serve loop is untouched. The census names
`handle` via `std/headers.fern:35`, `http_parse_request` via `std/stream.fern:36`
and a `strcat` / `str_slice` / `string_from_bytes_unchecked` tail; none of it is
attributed yet, and the growth is still unbounded, so #8003 stays open on that.

A second leak surfaced beside this one and is NOT this defect: a `var` declared
INSIDE a match arm takes its alias-binding retain and nothing releases it.

```fern
match (r) { Some(c) => { var kept: u8[] = c; chunk = kept; }, None => { … } }
```

leaks 90 blocks / 2080 B over 50 rounds, identically before and after this
change, and identically with the scrutinee BOUND to a local first — so no
scrutinee reclaim is involved. Hoisting the same `var` out of the arm balances
it. Filed separately; that is why the predicate excuses only the assignment and
not a `var` init, which could not be given a green gate until the arm-scoped
local is swept.
