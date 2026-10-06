# 2026-10-06 — the register-form passes copy from the first rewrite on

`ssa.register_form`, the pass battery every lifted function goes through
before register allocation, and the tables the allocator and the liveness
pass build per function. Refs #8171. No emitted byte changes: the compiler
before and after builds `checker.fern` for x86-64, arm64 and wasm, and
`fern.fern` for x86-64, byte for byte.

## What changed

Four of the battery's passes rebuilt every block and every instruction of
the function whether or not they changed anything. `prune_dead` appended
each kept instruction to a fresh list and each block to a fresh function;
`fuse_rotates` built a definition table over every value and copied the
whole function before finding that it held no two-operand `or` to fuse;
`thread_forwarding` rebuilt each block to carry a terminator that was
usually the one it had, then copied the reachable blocks and handed the
copy to `refreshed_preds`, which rebuilt every block again to carry a
predecessor list that was usually the one it had. Each copy was one
record and one argument array per instruction, one record per block, and
the release of the same when the old function went.

They now scan until the first rewrite and copy from there: a block's
instruction list is written from its first dead, fused or trimmed
instruction on, the block list from the first block that changes on, and
a pass that rewrites nothing returns its input. `fuse_rotates` looks for
a two-operand `or` before building its table and returns the function
when there is none. `refreshed_preds` compares the list it computed with
the one the block carries and keeps the block when they match.

Several per-value tables were built by appending a sentinel per value:
the allocator's eight interval and definition tables, the trivial-phi
map, the two block-position tables and the wrap pass's definition
tables. Each is now one zero-filled allocation of its final size and a
fill loop: a store per element, no growth checks and no regrowth copies.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 2e084b79 by the stage0 pin.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.862 G | 16.678 G (−1.09%) |
| `ssa.register_form` (its passes inline into it), self | 257.0 M | 196.4 M |
| `ssa.regalloc_linear`, self | 263.6 M | 243.7 M |
| `ssa.block_positions`, self | 78.0 M | 67.4 M |
| the `SBlock` drop, self | 69.6 M | 44.7 M |
| `__fern_arr_push`, self | 290.6 M | 269.5 M |
| `__fern_arr_dec`, self | 439.7 M | 425.4 M |

`prune_dead` alone, measured first, was −0.12%: most functions do lose an
instruction, so its gain is the blocks and prefixes it no longer copies
rather than whole functions returned as they came.

## What is left

`register_form` still spends most of its time in `prune_trivial_phis`,
which rewrites every instruction's argument list once any phi folds, and
in `thread_bool_joins`, whose `use_counts` and `truth_constants` tables
are rebuilt per round. `refreshed_preds` builds the predecessor lists
from scratch to compare them; the edges that change are the ones
`thread_forwarding` moved, and a pass that knew them could update only
those blocks' lists.
