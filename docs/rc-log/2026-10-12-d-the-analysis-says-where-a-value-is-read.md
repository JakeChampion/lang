# 2026-10-12 — the analysis says where a value is read

`ssasem.Uses` gains `block`, `at` and `slot`, and `ssasem.read_after`.
Refs #11910, the fourth slice: where each read sits.

## The shape

Four questions in `ssaunits` asked whether a value is read later in a block
or in a block that can run later, and each answered by scanning instructions:

- `used_after` and `read_after_direct` walked the rest of the block and
  tested every operand against the dependency chains.
- `needed_past_slot` walked the rest of the block for reads of the box.
- `named_after` scanned each block it entered for phi operands on the edge it
  came by (`phi_reads_on`) and for other reads (`block_reads`).

`ssasem.Uses` records, beside each operand's instruction kind, the block and
position of that instruction and the operand's index. For a phi, that index
names the edge the operand is read on. All four questions are now queries
over the uses of the value and of the values anchored to it (`anchored`).
`reads_source`, the direct relation's per-operand test, is gone.

## Measured

| | main | this |
|---|---|---|
| the whole compiler, compiled from main's sources | identical x86-64 assembly | identical |
| allocations compiling `checker.fern` | 23,533,737 | 23,541,239 (+0.03%) |
| `ssaunits.fern` lines | 5,144 | 5,136 |

The added allocations are the three new arrays built for every analysis.
Compile time for `checker.fern` is unchanged within run-to-run noise.
