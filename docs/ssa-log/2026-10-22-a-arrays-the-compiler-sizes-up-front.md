# 2026-10-22 — arrays the compiler sizes up front

`util.name_index`, `semsource.renumber`, `ssaunits.dying_reads`,
`ssaunits.steps_by_block`, `ssaunits.block_step_row`, `ssalive.set_ids`
and `ssa.regalloc_linear_fixed`. Refs #8171.

## The shape

The whole-compiler profile puts the two append kernels at 1.68 G Ir of
self time. Some of their callers know the final size of the array they
grow, or grow one only to read it once. Seven sites:

- `name_index` grew the bucket heads and the chain links from empty, one
  `-1` at a time, 539 K appends per self-compile.
- `renumber` grew the id map the same way, 945 K appends.
- `dying_reads` built `parents[u].append(u)` per operand, only to loop
  over it: one allocation and one copy of the parents per operand.
- `steps_by_block` grew each block's row by one step at a time, and
  `block_step_row` grew the row it returns twice over, 1.26 M appends
  for the six `step_index` calls per plan.
- `set_ids` grew its result by one value per set bit, 699 K appends.
- The register allocator built a four- or five-element candidate array
  per value it placed, through the shared append kernel for the first
  element and the owned one for the rest.

## What changed

`name_index`, `renumber` and `block_step_row` take their arrays from
`minus_ones` at the final length. `steps_by_block` counts the steps per
block first, allocates each row at its count and fills it through a take
of the row. `set_ids` allocates at the population count of its words.
`dying_reads` visits the parents and then the operand through
`dying_read`, with no array between. The allocator reads its candidates
from `candidate` by index. Every site produces the same values in the
same order.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is one frozen copy of main at ddc5b11, which replaces the copy at
2210347: the retirement of `__method_Array_` in #12933 made the old copy
uncompilable by a current compiler, so the series restarts here.

| subject | main at 42019bb | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 74.492 G | 74.393 G (−0.13%) |

The whole-compiler binary is byte-identical to the base compiler's.

## What is left

The expiry step of the register allocator still rebuilds its three
active-interval arrays through appends, because no primitive shortens an
array in place. `remove_reg`, `drop_active_at` and `drop_last_i` have the
same shape. A primitive that shortens an owned array in place can serve
all four. `Scope.bind` appends to a shared array per binding. It is the
largest caller of the shared kernel.
