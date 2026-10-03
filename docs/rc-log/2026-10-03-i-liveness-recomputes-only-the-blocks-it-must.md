# 2026-10-03 — liveness recomputes only the blocks it must

`ssalive.compute_checked` and `ssa.live_at_def`'s callers. Refs #8171. No emitted
byte changes: the stage0-built compiler before and after emits the fixed
older tree (`examples/self_host/fern.fern` at 1ae9cad, its bindings
spelled `let`) and that tree's `checker.fern` byte for byte.

## What the profile named

`ssalive.compute_checked` was 134 M of self cost in the stage-2 compile of
`checker.fern`. Every pass of its dataflow recomputed every block, and the
loop ran one full pass more than it needed, only to find that nothing had
changed.

`ssa.mate_interferes` was 67 M. Of that, 55 M came from `slot_hints`,
which asks it, for each spilled value whose phi mate is spilled too,
whether the two interfere; the rest came from two questions the register
loop of `regalloc_linear` asks about a mate. Each question walks the blocks
and reads every instruction. `assign_spill_slots` already answers the same
question for slot sharing (`2026-10-03-h`) from read sites, a table built
once per function.

## What changed

- **Liveness recomputes only dirty blocks.** A block is recomputed while
  it is dirty: at first, and again after a successor's live-in grows.
  Liveness only grows, so the fixpoint is the same and the confirming pass
  goes.
- **`live_at_def` is the one interference test.** `slot_hints` and the
  register loop's two mate questions ask it, and `mate_interferes` is
  deleted. `regalloc_linear` builds the read sites the first time one of
  them asks, and hands the same table to `assign_spill_slots`, which still
  builds one itself when nothing before it did.
- **A block's phis are defined together.** `live_at_def` answered that a
  value defined later in the other's block is not live at its definition.
  For two phis of one block that order means nothing; it now falls through
  to the read sites. Only `path_slot` can reach the change: elsewhere a mate
  is never defined after its value in the same block. `path_slot` also asks
  the other direction, which already answers that the two interfere unless
  the earlier phi is never read, so the answer moves only for a dead phi.

## Measured

The compiler each tree builds from itself through the pinned stage0
(stage 2), emitting the fixed tree's `checker.fern` under callgrind:

| | main at f48e75ab8 | this change |
|---|--:|--:|
| total Ir | 20.173 G | 20.084 G (−0.44%) |
| `ssalive.compute_checked`, self | 134 M | 75 M |
| `ssa.mate_interferes`, self | 67 M | — |
| `ssa.live_at_def` + `read_sites`, self | 57 M | 109 M |

## Witnessed

Both emit identities, on the final tree. The SSA allocation, liveness, dependency
verification and units tests (`TestSelfHostSpillSlotMates`,
`TestSelfHostSpilledMateSlot`, `TestSelfHostSSALifetime*`,
`TestSelfHostSSADependencyVerify*`, `TestSelfHostSSAUnits*`,
`TestSelfHostSSABackendAgreesWithNative`, `TestSelfHostSSAPhiCyclesAcrossSpills`,
`TestSelfHostSSAFrameIsSizedBySpills` and the merge-hint and eviction
tests) pass. So do `make check-sources`, the lint ratchet, and stage 2 ==
stage 3.
