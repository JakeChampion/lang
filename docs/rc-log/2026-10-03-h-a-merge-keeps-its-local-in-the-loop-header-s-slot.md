# 2026-10-03 — a merge keeps its local in the loop header's slot

`ssa.assign_spill_slots` (`path_slot`, `read_sites`, `live_at_def`) and
`ssadeps.analyze`. Refs #8171. The register allocator's output changes, so
there is no emit identity. The compiler this tree builds through the pinned
stage0 builds itself, and that build reproduces itself byte for byte.

## What the profile named

`ssa_lift.lift_impl` was the largest self cost in the stage-2 compile of
`checker.fern`: 943 M. Its loop body is an else-if chain over the op kinds,
and most of the locals it carries are spilled. The register side of the
merge cascade was fixed in `2026-10-03-f`. On the slot side, 848 slot-to-slot
copies remained, in runs of up to 29 on the merge edges, and they ran 199 M
of those 943 M.

The cause is the one `2026-10-03-f` measured for registers and did not land.
The loop header's phi for a local is read after the loop, so its interval
covers every arm and merge of the body. Its slot is never free at a merge,
so the merge phi and the arm's value each take another slot, and every edge
on which the local is not written copies the header phi's slot into the
merge's.

## What changed

- **Sharing by path.** When a spilled phi, or the spilled result of a
  two-address op, cannot have its mate's slot, it shares the slot of one of
  its operands. The condition is that no value in that slot is live where it
  is defined, nor it where they are. Two SSA values may share a home exactly
  then.
- **The question is answered from read sites.** `read_sites` records where
  each value is read, in one flat table with per-value offsets. A phi
  operand counts as read at its predecessor's terminator. `live_at_def`
  walks blocks forward from the definition, stopping at the other value's
  defining block.
- **Built only when needed.** The read sites are built the first time a
  function asks, so a function whose spilled values all keep their hints
  never builds them.
- **No second check in `ssadeps.analyze`.** It verifies its input with
  `ssalive.input_error`, then called `ssalive.compute`, which checked the
  same input again. It now calls `compute_checked`.

## Measured

The compiler each tree builds from itself through the pinned stage0
(stage 2), emitting the fixed tree's `checker.fern` under callgrind:

| | main at 2d4e84d47 | this change |
|---|--:|--:|
| total Ir | 20.584 G | 20.511 G (−0.36%) |
| `ssa_lift.lift_impl`, self | 943 M | 832 M |
| `ssalive.input_error`, self | 60.9 M | 36.0 M |
| `read_sites` + `live_at_def`, self | — | 55.9 M |

`lift_impl`'s slot-to-slot copies fall from 848 to 202. The whole
compiler's x86-64 text is 2,124 lines shorter.

The thirty `examples/bench` programs: −0.33% in total, with every exit
status unchanged. `record_update` is −3.8%, `pvec_with` −2.0% and the two
UTF-8 ingests −1.2%. The largest regression is `map_string`, +0.2%. The
merge-cascade reproducer goes from 9 slot-to-slot copies to none.

The first version kept an array of reads per value, and building it cost
50 M across the 191 functions that asked. The flat table is the version
above.

## Witnessed

The merge cascade loop of `TestSelfHostSSAMergePhiHintsADyingOperand` is
now a case of `TestSelfHostSpillSlotMates` (`merges`). It moves nothing
between slots and exits as the interpreter does; main copies nine. The
SSA allocation tests pass (`TestSelfHostSSABackendAgreesWithNative`,
`TestSelfHostSSAPhiCyclesAcrossSpills`, `TestSelfHostSSAFrameIsSizedBySpills`,
the merge-hint and eviction tests). So do `make check-sources`, the lint
ratchet, and stage 2 == stage 3.
