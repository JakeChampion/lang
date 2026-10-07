# 2026-10-07 — a closing scope drops the slots nothing reads again

`ssa_lift.lift_impl`, the `if`, loop and block folds; `last_reads`,
`read_horizon`. Refs #8171. Follows
2026-10-07-g-a-branch-records-only-the-slots-it-changed, whose "Left" named
the scope fold.

## Before

A scope keeps one row per slot written inside it: the slot, its value when
the scope opened, and the mark the first write found. When the scope closes,
its rows fold into the enclosing scope's, except a row for a slot the
enclosing scope had already written. A row therefore travels up through every
scope between the one that first wrote the slot and the outermost one, and
each merge on the way builds a phi for it wherever the paths disagree.

An `if` / `else if` chain is one scope nested in the next. A temporary written
in the twentieth arm was folded twenty times on the way out. Counted on a
compile of `checker.fern`, the folds visited 3,343,626 rows over 2,603 lifts.
2,489,171 of those visits (74%) were for a slot no op reads again, and the
largest function alone made 338,291. Every such row also produced phis that
the dead-value prune removed later.

## Change

Before the walk, `last_reads` records the last op that reads each slot: a
`load_local`, or a `dyn_dispatch` taking its arguments from a run of slots.
Those are the only ops that read one. When a scope closes at op `i`, a slot
whose last read is before `read_horizon` cannot be read again, so the fold
drops its row instead of carrying it out. `read_horizon` is `i`, unless a loop
is still open around the closing scope. In that case it is the op where the
outermost such loop opened, because its back edge runs the loop body again,
reads before `i` included.

A dropped slot has no row in the enclosing scope, so no merge there builds a
phi for it, and its value after that merge is never read. Every phi a merge
still builds is one that a later read needs, which is what the prune left
behind before. The emitted code is the same.

## Measured

`checker.fern` to an x86-64 binary under callgrind. Each side's stage 3 is
built by its own stage 2, with no `-g`. Both stage 3s rebuild themselves byte
for byte.

| | main (dc9d71511) | this change |
|---|--:|--:|
| total Ir | 14,453,352,543 | 14,214,726,572 (−1.65%) |

Byte-identical against a compiler built from main: `checker.fern` for
x86-64, arm64 and wasm32-wasi, and `fern.fern` for x86-64.

The saving is more than the fold cost. Beyond the scope that wrote a dead
slot, no merge or edge record sees its row, so the lift builds fewer phis
and the passes after it have fewer dead ones to prune.
