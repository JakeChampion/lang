# 2026-10-06 — the leaf splice and the phi fold copy from the first rewrite on

`ssa.inline_leaves`, which splices a leaf callee's instructions in place of
each direct call to it, and `ssa.prune_trivial_phis`, which drops the phis
that fold to one operand and rewrites their readers. Refs #8171. No emitted
byte changes: the compiler before and after builds `checker.fern` for
x86-64, arm64 and wasm, and `fern.fern` for x86-64, byte for byte.

## What changed

`inline_leaves` returned its input only for a module with no leaf at all.
With any leaf it rebuilt every block and appended every instruction of
every function, spliced or not, and its leaf lookup compared the callee's
name against each leaf's by string before checking the arity.
`prune_trivial_phis` returned its input only when no phi folded; otherwise
it built a fresh argument array and instruction record for every
instruction of the function, whether or not any operand moved, and a fresh
terminator for every block.

Both now copy from the first rewrite on, the shape `2026-10-06-r` gave the
rest of the register-form battery: a block's instruction list is written
from its first spliced call, dropped phi or rewritten instruction on, the
block list from the first block that changes on, and a function in which
nothing changes is returned as it came. An instruction whose operands all
stay is kept rather than rebuilt; the leaf lookup tests the arity first.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 2e084b79 by the stage0 pin.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.862 G | 16.778 G (−0.50%) |
| `ssa.inline_leaves`, self | 61.9 M | 43.1 M |
| `ssa.register_form` (prune_trivial_phis inlines into it), self | 257.0 M | 249.0 M |
| the `SBlock` drop, self | 69.6 M | 58.5 M |
| `__fern_arr_dec`, self | 439.7 M | 428.4 M |

## What is left

`inline_leaves` still builds a value map per spliced call by appending a
sentinel per leaf value; the leaves are at most twelve instructions, so
the map is small, and the remaining cost is the splice itself.
