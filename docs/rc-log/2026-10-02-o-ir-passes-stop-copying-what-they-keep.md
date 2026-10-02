# 2026-10-02 — the IR passes stop recopying what they keep

`ir.eliminate_dead_code`, `ir.propagate_copies`,
`ssa.mate_interferes`, `ssa.thread_forwarding`, `ssa.inst_has_effect`.
Refs #8171.

- **Worklists read through a cursor.** `mate_interferes` and
  `thread_forwarding` popped their reachability worklists with
  `drop_last_i`, which copies the array minus its last element. Reachability
  does not depend on visiting order, so both read the list through a cursor.
- **No arrays to test a kind.** `inst_has_effect` built two array literals
  on every call to compare the kind against; it compares directly.
- **Lazy copies.** `eliminate_dead_code` and `propagate_copies` rebuilt the
  whole op list even when they dropped nothing. Both now start writing a new
  list at the first op they drop (`fold_prefixed` copies the kept prefix, as
  in `fold_const_binaries`) and otherwise hand the input back.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container. Stage 2
is the compiler the self-host compiler builds from the same commit.

| | main (9b3b60c) | this change |
|---|--:|--:|
| stage 2, total Ir | 32.20 G | 31.78 G (−1.30%) |
| `ssa.drop_last_i` self Ir | 121.1 M | 2.4 M |
| `ssa.inst_has_effect` self Ir | 121.7 M | 28.2 M |
| `ir.eliminate_dead_code` self Ir | 171.0 M | 126.3 M |
| `ir.propagate_copies` self Ir | 144.7 M | 147.1 M |

The two lazy copies together took 121 M off the total, measured on the
previous main (c24af1f) against a build without them; `propagate_copies`' own cost is
unchanged (its two counting walks dominate it), and the saving is in the
allocation and pushes it no longer makes.

Byte-identical against a compiler built from main: the `checker.fern` binary,
the stage-2 compiler's output, and all 1,965 `selfhost-emit-hashes` rows.
