# The variant drop plan gets a payloadless arm (#7732)

#7732 reported a call-bound `Option[i32[]]` losing its release "as
soon as the producer has a `None` return", and attributed the strand
to the Some rounds. Splitting the rounds says otherwise:

| one round | allocs / frees / live |
|---|---|
| None only | **1 / 0 / 32** |
| Some only | 2 / 2 / 0 |

**The Some round was always clean; the None round leaks its rebox.**
The issue's 3200 B at 200 rounds is 100 None rounds × 32 B.

## The mechanism

`var o = mk(i)` reboxes the pair-form result through
`emitRepackPairAsHeapBox`, which materialises a REAL rc=1 box for
whatever tag the callee returned. The release is the inline
tag-switch drop (`emitEnumSlotDrop`'s variant-plan tier — the gen-fn
route declines a generic instantiation), and `enumVariantDropPlan`
skipped payloadless variants on the stated ground "payloadless ⇒
static sentinel, no heap box". True for direct construction — a bare
`None` IS the shared sentinel, and the is_unique gate declines it —
and falsified by the rebox. A unique None box matched no arm, fell
through the switch, and nothing freed it. The whole diff between the
leaking and the clean program is mk's None branch; `round` and every
drop are byte-identical, which is why reading the emitted release was
the step that found it.

The always-Some program balances because every box takes the Some
arm; that control hid the class.

## The fix

Payloadless variants get a tag arm with no payload loads and a
`__fern_box_free` at the enum's UNIFORM box size — which is the
rebox's own layout (`payloadWidth` pointer → 16 at ptrW 8, scalar
→ 8, matching `payloadLayout` for the payload variants). An enum with
no uniform size keeps the fall-through: no producer of a unique
payloadless box exists at a non-uniform size. An all-payloadless enum
still gets no plan, so the success condition mirrored by
`enumNeedsDrop` is unchanged, and `genEnumDropFn` — which shares the
plan — picks up the same arm for the `__drop_enum_` route.

Double-free analysis: the arm runs only under is_unique (rc == 1 —
sole reference), a sentinel declines on its high bit before the
switch, and direct construction of a payloadless variant never
allocates — so the only value the new arm can free is a rebox with no
other owner.

## Measured

x86-64 and arm64, the issue's exact pair: 300/200 live 3200 →
**300/300 live 0**, answers unchanged and equal to interp; the
always-Some control stays 400/400. `two_nones_share_sentinel` in the
conformance census drops 2 → 0 and is re-banked; certify stays at
zero with it in the clean set. Non-vacuous both ways: without the fix
the new plan unit test fails ("the payloadless variant has no arm")
and the probe re-leaks at the filed figures.

Per `docs/NATIVE-CONVERGENCE.md` this is native catching up to the
self-host, whose `opt_fresh_ret_fns` → tag-guarded payload drop
already handled the shape — a #4451 debt item paid rather than added.
