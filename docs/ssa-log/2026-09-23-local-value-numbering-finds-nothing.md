# Block-local value numbering finds nothing to merge

No slice landed from this. It is here so nobody builds it twice.

`ssa.optimize` (deleted the same day) carried a CSE pass that never ran on
the production lift. Its working part was rebuilt directly for the lifted
function and measured: `value_number`, run between `prune_trivial_phis` and
`prune_dead` on both native emitters, merged an instruction with an earlier
one in the same block when the kind, operator, immediate and operands
matched — integer and float arithmetic, the width ops, `ld_at`, `ld_idx`,
`ld_byte` and the checked element and byte reads — with every candidate
ended by anything that may write memory or call out (so no merged value's
interval grows across a call). Candidates were chained per first operand,
so it cost one linear walk.

## Measured

x86-64 static instructions, self-host CLI built from the same tree with and
without the pass, 2026-09-23:

| subject | without | with | saved |
|---|---|---|---|
| `examples/self_host/checker.fern` | 479,522 | 479,303 | 219 (0.05%) |
| `examples/bench`, all 29 programs | 84,055 | 84,041 | 14 |

Compile time of `checker.fern` moved inside the noise (12.85 s against
12.66 s, one run each).

## Why there is nothing there

The duplicates a reader sees in this backend's output are not duplicate
instructions in the lifted function:

- A loop's `i < xs.len()` loads the length in the header block, and the
  checked read in the body loads it again inside `arr_at`'s own sequence —
  one SSA instruction, not two. Bounds-check elision removes that reload;
  value numbering cannot see it.
- The compiler's code is call-dense, and a call ends every candidate. Letting
  pure arithmetic survive a call would move the cost into a callee-saved
  register or a spill rather than remove it.
- The toy that motivated it — `p.x * p.x + p.y * p.y` loading each field
  twice — does merge (two loads fewer), and is about all that does.

Range-based bounds-check elimination and the register-argument convention
are where the redundant memory traffic actually is.
