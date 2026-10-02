# 2026-10-02 — three passes stop rescanning what they already know

`ssalayout.representatives`, `ssaunits.grow_rows` / `named_after`, and
`ir.fold_const_binaries`. Refs #8171.

- **Loop representatives.** `representatives` asked `representative` for every
  position, and `representative` scanned every block for an outermost header
  holding it: n² per region, with `outermost` tested inside. It now walks the
  outermost headers once, in ascending order, and gives each position it holds
  the first header to claim it, which is the header the scan returned.
- **Block positions.** `ssaunits` found a step's block with a linear
  `block_index` search, once per plan step and again per `named_after`
  successor. `grow_rows` now builds the id-to-position map once per function
  and `named_after` reads the one its flow already carries
  (`ssalive.position`). The map is `ssa.block_positions`, now public and
  keeping the first block for an id, which is what the search returned. The
  copies in `ssalive` (`first_positions`) and in both SSA emitters
  (`ssa_block_index`) are deleted.
- **Constant folding.** Every fold arm starts at a readable i32 or i64 constant
  or a `const_str`, but each position walked all nine arms' guards before
  falling through, and copied the three ops a binary fold reads first. A
  position that starts none of them is now copied on at once.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
is the compiler the self-host compiler builds from the same commit.

| | main (b23034a) | this change |
|---|--:|--:|
| stage 2, total Ir | 34.95 G | 34.14 G (−2.32%) |
| `ssalayout.representative` self Ir | 275.0 M | — |
| `ssaunits.block_index` self Ir | 209.7 M | — |
| `ssa.block_positions` self Ir | 23.3 M | 52.2 M |
| `ir.fold_const_binaries` self Ir | 497.9 M | 146.4 M |

Byte-identical against a compiler built from main: the `checker.fern` binary,
the stage-2 compiler, and all 1,965 `selfhost-emit-hashes` rows.

`ssarc.block_index` (46.9 M) is the last linear block search on the profile;
its callers have no position map in reach yet.
