# 2026-10-06 — a phi takes the home of its fall-through operand

SSA register allocator (`ssa.phi_mates`, `regalloc_linear`), x86-64 and
arm64. Refs #8171.

## The shape

An else-if chain inside a loop, where most arms write the same few values:

```fern
while (i < ops.len()) {
  let k: i32 = ops[i];
  if (k == 0) { v0 = v0 + k; stack = stack.append(k); n = n + 1; }
  else if (k == 1) { v1 = v1 + k; stack = stack.append(k); n = n + 1; }
  ...
  i = i + 1;
}
```

The chain lowers to one nested merge per arm, laid out innermost first, each
falling through to the next. A merge has a phi for every value some arm at
its depth or below wrote, so `ssa_lift.lift_impl`, sixty arms over
forty-eight loop-carried values, has forty-eight phis at nearly every one of
its twenty-five merges. Each phi has two operands: the arm's, and the inner
merge's phi.

`phi_mates` hinted a phi at its first operand from before it, which is the
arm's: the merge took the arm's home, and the value arriving from the inner
merge moved on every path through it. The homes rotated down the chain, so
the fall-through from one merge to the next was a permutation of a dozen
registers and a frame-to-frame copy of the spilled rest, 13 to 50 moves a
merge, and an op that ran a deep arm paid every merge on its way out.
`2026-10-05-z` covers a different operand: the loop header's phi, which
outlives the merge.

## The change

Among a phi's operands from before it that die by it, `phi_mates` now hints
at the one arriving from the block laid out just before this one, which
falls through into it. The straight-line path moves nothing; an arm's edge,
which jumps, pays the one move when that arm runs. A merge no block falls
into keeps its first operand.

A phi so hinted whose operand is spilled stays spilled too, taking its slot
(`hinted_slot`). Loaded into a register instead, it took one freed by the
inner merge's phis, which a later phi of the same block was still hinted
at, and the hints of the whole block failed in a chain.

## What was tried first

Counting the paths that reach each predecessor through the forward edges,
to prefer the busier one: the inner merge carries every deeper arm where
the arm carries one. The count explodes inside an arm, where each `append`
is a uniqueness test and a join, so an arm with three of them outweighed a
merge of twenty, and the hint went to the arm as before. Static branch
probabilities (a half each way) tie at every level of the chain, and the
chain's tests are string compares, which no opcode heuristic reads. The
layout is what the lift and the emitter agree on, and it is the fact that
holds.

## Measured

The chain above with twenty arms, built for x86-64, counting the moves in
the longest run of fall-through merge blocks in `f`:

| | main | fall-through operand |
|---|--:|--:|
| moves in `f`'s merge chain | 24 | 1 |
| moves in `f` | 861 | 886 |

`ssa_lift.lift_impl` as the stage-3 compiler emits it for x86-64:

| | main | fall-through operand |
|---|--:|--:|
| moves in its twenty-five merge blocks | 475 | 28 |
| moves in the function | 4,814 | 4,268 |
| instructions in the function | 13,747 | 13,200 |

`checker.fern` built for x86-64-linux under callgrind, main at 2c1e37d6
against this branch. Stage 2 is built by the fixed stage-1 compiler, so it
measures the allocator's own cost; stage 3 is built by stage 2, so it
measures the code the allocator produces:

| | main | fall-through operand |
|---|--:|--:|
| stage 3, total Ir | 18.446 G | 18.370 G (−0.41%) |
| stage 3, `ssa_lift.lift_impl`, self | 976 M | 881 M |
| stage 2, total Ir | 18.263 G | 18.299 G (+0.20%) |

The allocator's own cost is in `live_at_def`, the path query behind a slot
shared between a spilled phi and its operand (`slot_hints`, `path_slot`):
the new hint sends many more spilled phis there, and the query walked every
block the loop reaches to find no read. A value read only in its own block
is dead at any other block's definition, since a path back to a read enters
that block at its top and defines the value again first; `live_at_def` now
answers that without the walk, which took the rise from +0.34% to +0.20%.
What remains is the queries that do walk, 31 M, and `phi_mates` no longer
inlined, 25 M against the 25 M of `sole_readers` that is.

The stage-3 compiler reproduces itself: stage 3 and stage 4 are
byte-identical for x86-64 (10,865,480 bytes). `scripts/selfhost-emit-hashes`
differs from main on 626 of its 2,004 rows, 314 x86-64 and 312 arm64, with
the wasm rows and the 252 refusals unchanged.

## Witnessed

`TestSelfHostSSAMergeChainMovesNothing` (new), which builds the chain above
and runs it on every target, `TestSelfHostSSAJoinPhiSharesTheHeaderHome`,
and the SSA, spill and join suites.
