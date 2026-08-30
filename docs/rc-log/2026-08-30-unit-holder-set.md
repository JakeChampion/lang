# The certifier's unit-holder set, and three classes it named (2026-08-30)

`2026-08-30-certifier-oracle-first-results.md` ended on a rewrite rather
than a refinement: seven attempts to improve the static leak walk by
adding a rule had between them removed 0.8% of its false positives, and
the conclusion was that it "wants rebuilding against the oracle from a
correct unit-holder set, one named class at a time".

This is that rebuild. `internal/ssa/units.go` is the unit-holder set,
`internal/ssa/certify.go` is the walk on it, and
`internal/e2e/certify_oracle_test.go` is the comparison against the
census.

## What the probe was doing

One line of it, from `join_width_test.go`'s `pointerish`, which the
probe shared:

```go
if o.Kind != OpPhi && (o.Addr || o.Kind == OpAlloc) { out[id] = true }
```

— and then every such value was marked as holding a unit at its
definition. `Op.Addr` answers a REGISTER-WIDTH question: must the
backend skip its sign-extension. Every one of its over-approximations
is a false unit holder. `OpLoad` is marked unconditionally, because an
i64 and a pointer are indistinguishable there. `usize` is an address.
`base + fieldOffset` is an address. `OpConstString`, `OpEnumSentinel`
and `OpConstVtable` are addresses with an immortal rc header — the word
at `[ptr-8]` is `0x80000000` and every helper on every backend
short-circuits on that bit.

`UnitsOf` classifies by op KIND, the rc signature table and the solved
callee signatures instead, into six named origins: fresh, transferred,
borrowed, merged, unknown, none. A value it cannot place is `unknown`
and is counted, not guessed at — the probe's own record says guessing
"owned" for call results made its leak count 70% worse.

## The measurement

323 census-clean conformance fixtures, x86-64, reclaim on.

| | flagged | note |
| --- | --- | --- |
| probe (recorded 2026-08-29) | 18,638 functions, 20.3% | no pass battery, no dead-function cull |
| rebuilt walk, first run | 17.83% | |
| + interior addresses resolved to their base | 7.03% | |
| + the backend's IR pass battery | 6.07% | |
| + the backend's dead-function cull | **8.49%** | 62 of 730 |

**The 20.3% and the 8.49% are not the same measurement** and the table
should not be read as a single ratchet. The last two rows change the
PROGRAM, not the walk: once the battery and the cull run, 1,394 of the
2,124 functions the earlier rows walked are gone, and they were
disproportionately clean ones, so the rate rises while the finding count
falls from 543 to 314. Comparing against the probe on equal terms is not
possible — the probe's population was never recorded.

## The three classes

**Interior addresses were the whole of the first drop, and they are a
correctness fix rather than a filter.** The rc header sits BELOW the
pointer a program passes around, so an object's head is routinely
`alloc + N`:

```
v4  = alloc 16
v15 = add v4, 16        ; the object head
v30 = alloc 24
v33 = add v30, 8
store v33, v15          ; v15 moves into the container
v37 = add v30, 8
ret v37                 ; v30 leaves through the return
```

A walk keyed on `v4` and `v30` sees neither the store nor the return,
because both name a derived value. Resolving `base + const` to its base
took the rate from 17.83% to 7.03% — and the same one line is why the
store and the return rules work at all.

**Static sentinels** — the class the oracle named first
(`enum_sentinel x106` in one `checked_abs`) — are `UnitNone` by
construction and no longer appear.

**Two classes remain open**, and both are in the gate's failure message
so the next person starts from the breakdown rather than from a total:
`make_closure` (156 findings) and `alloc` (151). For the closures it is
genuinely unsettled which side is wrong: a closure cell is 32 bytes from
`__fern_alloc_rc1` at rc=1, lowering does not always emit its release,
and closure reclamation is already on `docs/TEST-GATES.md`'s live gap
list.

## Two things the harness found that are not about the walk

**The oracle observes the program the BACKEND emits, and the raw
lowering is a different program.** `InlineZeroCaptureClosures` rewrites a
zero-capture closure passed as a function argument into `OpConstFunc`, a
static `.rodata` cell — so the 32-byte block the raw lowering builds does
not exist in the emitted binary. Walking the raw lowering reported 419
closures as leaked, every one of them an object the backend had already
deleted. Any future comparison against the census has to run the pass
battery and the dead-function cull; `nativePassBattery` in the test
mirrors `x86_64.emitCollecting` for that, and a shared entry point in
`internal/ir` is the right shape and is the follow-up.

**The pass battery costs the lift two thirds of its coverage.** On the
raw lowering 37 functions of this corpus fail to lift; after the battery
360 do, all of them `OpBlock at op[N] non-void BlockType` — the battery
produces value-typed blocks `ssa.LiftFromIR` refuses outright. Every
existing lift-coverage figure, including #7803's 100.00%, was taken on
the raw lowering. That is a lead for #7803, measured here by accident.

## Also landed

`ssa.LiftProgram` — the whole-program lift entry #7786 called "one API
blocker". Three copies of the loop existed and they did not agree on
whether to pass call shapes. It builds `ir.CallShapes`, runs
`ResolveWidths` (which is what makes `Op.Addr` mean anything at all) and
NOT `Optimize` (which would break `Op.SrcOp` provenance), and returns
lift failures rather than dropping them.

## Next lead

`UnitUnknown` is 1,527 call results over these fixtures. That is the
result axis of the ownership signature table — `rcsigs.go`'s header says
outright that "whether the RESULT carries a unit is not modelled", and
#7786's own definition of the table asks for it ("whether its return
carries an ownership unit"). Phase B answers it for the 84 functions it
can prove hand back a borrow; nothing answers it for an allocator.
