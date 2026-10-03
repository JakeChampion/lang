# 2026-10-03 — a merge phi shares a home with an operand that dies

`ssa.phi_mates` and the eviction in `ssa.regalloc_linear`. Refs #8171.
The register allocator's output changes, so there is no emit identity;
the compiler this tree builds through the pinned stage0 builds itself, and
that build reproduces itself byte for byte.

## What the profile named

The first of the two causes in "Next: the merge cascade moves every local"
(`2026-10-03-b`): `phi_mates` hinted a phi at the first operand defined
before it, and at every merge of an else-if chain inside a loop that
operand is the loop header's phi, live through the whole body. The hint
could never be taken, so the merge phi went wherever was free and the
arm's values were moved, or stored and read straight back, on every edge.

## What changed

- When the first operand from before a phi outlives it, the hint is the
  first operand that dies by the phi instead. A loop-carried phi keeps its
  hint: the earlier attempt, which also moved those, made a sixteen-local
  else-if loop worse.
- An eviction no longer spills every value that shared the evicted
  register, only those whose interval still reaches the evicting value's
  start; one whose interval ended keeps the register, and the edge to its
  successor moves instead. Before, a finished value was spilled too, so
  the spill weight of a register charged for references that were already
  over.

## Measured

The compiler each tree builds from itself through the pinned stage0
(stage 2), emitting the fixed tree's `checker.fern` under callgrind, on
main at e27069257:

| | main | this change |
|---|--:|--:|
| total Ir | 21.096 G | 20.843 G (−1.20%) |
| `ssa_lift.lift_impl`, self | 1.122 G | 0.943 G |
| `ssa.mate_interferes`, self | 70.7 M | 38.8 M |
| stage-2 driver | 12,355,208 B | 12,338,976 B |

The thirty `examples/bench` programs built `-O` for x86-64 by each
stage0-built driver: 2.9078 G Ir in total either way (−0.0001%), with every
exit status unchanged. The largest moves are `map_string` +0.22% and
`sort_ints` −0.25%.

The merge-cascade reproducer (sixteen `i64` locals, a five-way else-if
inside a loop, 200,000 turns) goes from 16.64 M to 14.08 M Ir, and its
slot-to-slot copies from 13 to 9.

## Witnessed

`TestSelfHostSSAMergePhiHintsADyingOperand` (no arm of that loop stores a
sum and reads it straight back; main stores and reloads three) and
`TestSelfHostSSAEvictionSpillsEveryLiveSharer`; the 709 targeted
`internal/e2eselfhost` tests of the earlier entries; `make check-sources`
and the lint ratchet; stage 2 and stage 3 byte for byte.

## The trap

The first cut stopped the eviction's walk at the first value whose
interval had ended. The values that share a register are not ordered by
where their intervals end, though: a loop-carried value takes its phi's
register while the phi is still live, and the register's active end is the
later of the two. So a live value could follow a finished one, stay in
the register, and be overwritten by the value that evicted the chain.
Every benchmark, both reproducers and the stage0-built driver ran
correctly; the stage-2 driver crashed in the allocator during tree
shaking, and `ssa.prune_mark` lifted into a program on its own crashed too.
`still_holds` is asked of every value on the chain.

## Next

The second cause stands: when the hint is an operand that dies at the
edge, two phis of one merge can still want one register, an arm's value
and the inner merge's phi having sat in it on disjoint paths, and the
first phi placed takes it. A four-local else-if loop with no spills shows
it alone: seventeen register-to-register moves, before this change and
after.

What fixes it was measured and not landed. The loop header's phi is read
after the loop, so its interval covers every arm and merge although no arm
reads it, and only a path question can let an arm's value or a merge phi
share its register: two values may share when neither is live where the
other is defined. Asked of every value on the register's chain, in both
directions, that takes the four-local loop from seventeen moves to five,
none between the first arm and the back edge, and the thirty benchmarks
−0.14% (`sort_ints` −3.4%). It costs the compiler more than it saves:

| stage-2 emit of `checker.fern` | Ir |
|---|--:|
| this change | 20.843 G |
| answered by a block walk over every instruction per question | 21.136 G |
| answered from `ssalive.compute` live-out rows per function | 21.536 G |
| answered from per-value read sites and a block walk | 20.944 G |

The last two agree byte for byte on `checker.fern`. `lift_impl` does not
move under any of them: its locals spill, and the slots already have
their own sharing. The question needs to cost close to nothing per
function before it pays: built only for a function with a merge phi whose
hint fails, say, and flat rather than an array per value.
