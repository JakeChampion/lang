# 2026-10-21 — a merge orders its phi rows by a scan over their slot range

Both native backends. `ssa_lift.lift_impl`, the block merge. Refs #8171.

## The shape

When a block's edges meet, the lift gives each slot the edges disagree on a
phi, in slot order. It kept the rows that need one in a list ordered by
slot and inserted each new row by shifting the list down: an insertion
sort, quadratic in the rows of one merge. `parser.parse_stmt_at` merges
hundreds of slots at once, so on the whole compiler's self-compile the two
shift loops were 451 M instructions (0.58%), seventy times what the
`checker.fern` compile pays for a subject five times smaller. The
`checker.fern` profile never showed it.

## What changed

The merge already maps each row's slot to its row in `slot_pos`. It now
collects the rows that need a phi in row order with the slot range they
span, and orders them by a walk over that range, taking each slot
`slot_pos` still names. When the range is wider than the square of the row
count, as it is for a merge of two or three rows in a wide frame, the few
rows are sorted as before instead. The phis come out in the same slot order
either way.

A first cut scanned the range always. It measured −0.53% on the whole
compiler and +0.01% on `checker.fern`, whose merges have few phi rows and
wide slot ranges; the hybrid takes both.

## Measured

Under callgrind, private certifier memo warmed by one compile of the
subject, each compiler built by itself from its own tree, compiling one
frozen copy of main at 2210347. The base is #12883's head.

| subject | base | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 77.566 G | 77.101 G (−0.60%) |
| `compiler/checker.fern` | 16.034 G | 16.023 G (−0.07%) |

Both binaries are byte-identical to the base compiler's.
