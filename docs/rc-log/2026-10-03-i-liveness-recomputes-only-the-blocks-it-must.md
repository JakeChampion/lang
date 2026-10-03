# 2026-10-03 — liveness recomputes only the blocks it must

`ssalive.compute_checked` and `ssa.slot_hints`. Refs #8171. No emitted
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
whether the two interfere. Each question walks the blocks and reads every
instruction. `assign_spill_slots` already answers the same question for
slot sharing (`2026-10-03-h`) from read sites, a table built once per
function.

## What changed

- **Liveness recomputes only dirty blocks.** A block is recomputed while
  it is dirty: at first, and again after a successor's live-in grows.
  Liveness only grows, so the fixpoint is the same and the confirming pass
  goes.
- **`slot_hints` asks `live_at_def`.** `regalloc_linear` builds the read
  sites when some spilled value has a spilled mate to ask about, and hands
  the same table to `assign_spill_slots`. That function still builds one
  itself when no hint needed it.

## Measured

The compiler each tree builds from itself through the pinned stage0
(stage 2), emitting the fixed tree's `checker.fern` under callgrind:

| | main at b4e5a6c3a | this change |
|---|--:|--:|
| total Ir | 20.173 G | 20.085 G (−0.44%) |
| `ssalive.compute_checked`, self | 134 M | 75 M |
| `ssa.mate_interferes`, self | 67 M | 29 M |
| `ssa.live_at_def` + `read_sites`, self | 57 M | 83 M |

## Witnessed

Both emit identities. The SSA allocation, liveness, dependency
verification and units tests (`TestSelfHostSpillSlotMates`,
`TestSelfHostSpilledMateSlot`, `TestSelfHostSSALifetime*`,
`TestSelfHostSSADependencyVerify*`, `TestSelfHostSSAUnits*`,
`TestSelfHostSSABackendAgreesWithNative` and the merge-hint and eviction
tests) pass. So do `make check-sources`, the lint ratchet, and stage 2 ==
stage 3.
