# 2026-10-13 — slot sharing reads liveness off bitsets

Target-independent: `ssa.assign_spill_slots`, `path_slot`, `live_at_def`
split into `live_near` and its walk, `SpillLive`, `spill_live`,
`spill_live_at`. Refs #8171.

## The shape

When a spilled value may share a slot that another spilled value occupies,
`path_slot` asks `live_at_def` twice: is either value live where the other is
defined?

The first answer comes from inside the defining block. Past that,
`live_at_def` answered by walking every block reachable from the definition
without passing the other's own definition. Each walk:
- allocated and zeroed a two-word-per-block scratch array;
- rescanned the value's reads at every block it reached.

On a stage-2 compile of `checker.fern` that was about 7,900 walks reaching
150 blocks each. A splice that grows a caller multiplies both factors. When
#12344 let a once-called box splice past the caller's budget, `live_at_def`
alone grew by 80 M instructions, most of that merge's 1.2% on this compile.

## What changed

The walk's question is liveness. `spill_live` now computes, once per
`assign_spill_slots` call and only when slot sharing first needs it, each
block's live-in set over the values being given slots:

- `live_in(b) = use(b) ∪ (live_out(b) − def(b))`, to the fixed point,
  iterating the blocks backwards.
- A value is used in every block that reads it except its own. There it is
  defined before any read, which is exactly the block the walk refuses to
  enter.
- A parameter has no defining block and is killed nowhere.

`spill_live_at` keeps `live_at_def`'s in-block cases (`live_near`) and
answers the rest with one bit test per successor of the definition's block.
A value outside the set falls back to the walk. The other callers of
`live_at_def` are unchanged.

## Measured

The stage-2 compile of `checker.fern` under callgrind, without `-g`, warm
certifier memo, both compilers built from the same main:

| | Ir |
|---|--:|
| the owned first append | 14.287 G |
| bitset liveness for slot sharing | 14.159 G (−0.89%) |

Allocation is unchanged. A compiler that checked every `spill_live_at`
answer against the walk, and stopped on any difference, compiled the whole
compiler without one, so slot sharing chose the same slots and the emitted
code is the same. The stage-2 compiler recompiles itself byte for byte.

## Witnessed

These pass:

- `TestSelfHostOptimisationShapes` (`spill_the_cold_value`,
  `spill_the_long_value`) and `TestSelfHostSpilledMateSlot`;
- every `TestSelfHostSSA*` test;
- the lint ratchet and `make fmt-check`.
