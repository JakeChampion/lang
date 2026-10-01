# The lift tracks the slots each scope writes

`ssa_lift.lift_impl` merged slot values by the function's slot count: an `if`
copied every slot into a snapshot row, `else` copied every slot into a
then-arm row and restored every slot, a `br` recorded every slot on its edge,
a loop header got a phi per slot, and every `end` compared every slot.
Compiling `checker.fern`, that was 24 M slot visits for 86 k actual slot
writes, and 2.3 M SSA values for 579 k ops: `parser.parse_stmt_at` has 1,424
slots and 102 loops, so its header phis alone were 145 k values, each one an
instruction `prune_trivial_phis` then folded away and a row in every
value-indexed table `regalloc_linear` allocates.

The lift now records writes instead. `slot_mark[s]` is the deepest open scope
that wrote slot s; a scope's entries (the slot, its value when the scope
opened, the mark the first write found) sit on one stack from `sc_dl_from`,
and a closing scope folds them into its parent's. An `if` snapshots nothing:
at `else` the then-arm's entries move aside with their exit values and the
slots go back to their pre-if values; at `end` the union of the two arms'
entries is the candidate set, visited in slot order over the marked range.
A `br` records the target scope's written slots only; a slot an edge does not
list held the value at the scope's open. A loop header carries a phi only for
the slots a pre-scan of its body finds a store or tee for (`loop_writes`).

The phis a merge creates, and their order, are unchanged: a slot no arm wrote
had the same value on every path, so the dense pass created no phi for it,
and a loop-header phi for an unwritten slot was trivial. What moves is the
value numbering, since the unwritten slots' header phis no longer take ids.

Stage-2 (self-host-built) compilers from main 768a697, compiling
`checker.fern` to an x86-64 binary on a 4-core x86-64 container:

| | main | this change |
|---|--:|--:|
| callgrind Ir | 61,295,055,956 | 56,077,705,735 (−8.5%) |
| `lift_impl` self | 1,791,492,041 | 1,062,160,655 |
| `ssa.regalloc_linear` self | 834,935,691 | 267,774,671 |
| `ssa.trivial_phi_repl` + `prune_trivial_phis` + `resolve_repl` + `phi_sole_operand` | 734,545,571 | 45,524,257 |
| wall, median of 3 interleaved | 11.31 s | 10.59 s |
| peak RSS | 1,237 MB | 1,132 MB |

The emitted `checker.fern` binary is byte-identical, the whole compiler built
from the unchanged sources by the new compiler is byte-identical to the base
stage 2, and `scripts/selfhost-emit-hashes` matches on all 1,935 rows across
the three targets. The emitted asm TEXT is not identical: the rc-guard labels
carry the value id (`.Lssa_<fn>_rcdeck289`), and those ids shift. The
assembled bytes are the gate, not the text.

A first cut sorted each merge's candidate set with a heap sort, which cost
0.70 G on its own: a slot written in a nested scope is a candidate at every
enclosing merge, so the outer merges of a large function sort hundreds of
entries. The candidates are already marked in a per-slot scratch, so the
merge scans the marked range instead, at a few instructions per slot.
`TestSelfHostPerModuleEmitAllFixpointX86_64`, the gate that caught #8200's
arena regression behind byte-identical output, passes.
