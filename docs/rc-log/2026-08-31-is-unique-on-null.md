# 54% of uniqueness guards test a null pointer (2026-08-31)

#7787 asks for the opposite of what landed here. It wants to prove a
guard TRUE and delete the check — a codegen change whose failure mode is
a destructive write to a shared box, invisible to every runtime detector
until the mis-paired path executes. Four instrument errors are already
recorded on that issue, and the rule they produced is the one this note
followed: **no aggregate is publishable until one instance of it has
been read end to end.**

Reading one instance is what found this.

## The shape

Over the conformance corpus, AFTER `OptimizeCleanup` has already run:

```
   [ 43] local.store 0
   [ 45] const.i32 0
>> [ 46] rc.is_unique __fern_rc_is_unique argc=1
   [ 47] if void
   [ 48] const.i32 0
   [ 49] const.i32 16
   [ 50] call __fern_box_free argc=2
   [ 52] else
   [ 53] const.i32 0
   [ 54] rc.dec __fern_rc_dec argc=1
   [ 56] end
```

The operand is a literal `const.i32 0`. `OpRcIsUnique`'s own declaration
states the contract — *"(ptr) → i32 (1 iff rc == 1; sentinel/static →
0)"* — and every backend's helper opens with a null guard, so the test
is 0 without exception. The reclaim arm is unreachable and the block
frees a null pointer.

## Why nothing had removed it

Not a missing analysis. `ConstPropagate` already tracks constants
through locals and across `if` scopes — its own doc gives
`if (keyKind == 0)` as the shape it exists to expose — and it was
already delivering the zero to the guard. **`Fold` simply had no rule
for the op.** Measured after cleanup: 8,456 of 15,582 guards sat on a
literal `const.i32 0` with nothing folding them.

One peephole, `const.i32 0; rc.is_unique` → `const.i32 0`, hands the
condition to `pruneConstIf`, which was already there.

| conformance corpus, after cleanup | before | after |
| --- | --- | --- |
| `is_unique` guards | 15,582 | **7,126** |
| guards on a literal null | 8,456 | **0** |
| total ops | 8,436,784 | **8,338,519** (−98,265) |

## Where the shape comes from

Lowering emits an `is_unique`-gated deep drop for a droppable local at
scope exit whether or not the slot was ever assigned on the path that
reaches it. The zero-init prologue store is then the only store the
guard can see. That is a reasonable thing for lowering to do — it cannot
know per-path what `ConstPropagate`'s scope merge later proves — so the
division of labour is right and the fix belongs in the folder, which is
the pass that owns "this operand is a known constant".

## Why this is safe where #7787 is not

Removing an unreachable block changes no behaviour; eliding a
true-proven guard writes into a box nothing proved unique. The two are
not the same transform and should not share the "14,313 guards" framing.

Witnessed rather than argued. These were DROP blocks, so a live one
removed would change memory behaviour, and two gates pin that exactly:

- `TestConformanceLeakCensusX86_64` — all 453 fixtures, per-fixture
  unpaired-allocation counts, failing on more **or fewer**;
- `TestX86_64RcCorpusLeakGate` — 216 programs, per-case `live_bytes`
  pinned on both backends.

Both unchanged, plus `TestFernFixtures` and the whole of
`internal/ir`. A drop that actually ran could not have survived that.

## The caveat that does not apply here, and would to the next step

#7787's third correction records that "no real store precedes it in op
order" is sound only outside loops — inside one, a later store is
earlier on the next iteration, and 22 guards program-wide are in loops.
This transform does not rely on that argument at all: it asks
`ConstPropagate` what the operand is, and that pass already clears its
tracking at `OpLoop` entry and invalidates every slot written inside the
loop at `OpEnd`. The loop caveat is inherited as handled rather than
re-reasoned.

## What is left of #7787

The remaining 7,126 guards are the population the issue is actually
about, and the previously-measured headroom over them is still 0 by
single-static-assignment reasoning. Nothing here changes that; it
removes the half of the population that was never the target.
