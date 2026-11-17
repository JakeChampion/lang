# A phi web saves instructions, not time

2026-11-17. x86-64 and arm64. No code changes. This entry records an attempt
in `ssa.regalloc_linear_fixed_costs` that did not land, and why.

## The shape

In `ins` of rbtree, a node with one count gives its two children to the
phis of the join. Each child is a load that only its phi reads. The phi
lives across the recursive call, so it is in a saved register. The load went
to a caller register, and the edge moved it to the home of the phi:

```
movq 8(%r14), %rax
movq 16(%r14), %rdi
movq %rax, %r12
movq %rdi, %r15
```

## The attempt

A web is a phi with one operand that only the phi reads, and whose interval
ends before the phi. The scan gave the operand the interval, the weight and
the call needs of the two. The phi then took the home of the operand, and
the edge moved nothing. Each form had these limits:

- A phi with an operand that outlives it makes no web. That phi has a
  better mate.
- The phi takes the home of a mate first, and of its web only without one.
- A web does not go to the frame. When it loses its register, the operand
  goes on alone.
- When the phi cannot join, the operand gives back the rest of its interval.

## Two faults on the way

- The first form let an operand from outside a loop join a phi in the
  loop. The edge of that operand runs again on each iteration, so the
  operand must keep its value. The compiler that this form built parsed
  `1.7976931348623157e308` as infinity. The miscompiled function was
  `util.parse_f64_bits`. The limit on the interval of the operand is the fix.
- `ssa_parallel_moves` writes a cycle of two registers as one `xchgq`.

## Measured

Instructions on x86-64, `scripts/perf-bench` against main at `dc3e50cf`:

| row | the best form | the best form, no `xchgq` |
|---|---:|---:|
| perceus_rbtree_ck | -2.4% | -2.4% |
| perceus_rbtree | -2.2% | -1.1% |
| string_scan | -1.7% | -1.7% |
| perceus_cfold | -1.6% | 0.0% |
| pvec_with | +0.7% | +0.7% |
| sort_ints | +1.9% | +1.9% |

On arm64 under qemu, the best form gave these changes: rbtree -2.0%,
rbtree-ck -0.9%, cfold -1.3%, deriv +1.4%.

Processor time on x86-64 told a different story. With the best form,
rbtree took 7% more time than main, and so did cfold, over 30 and 40 runs.
A sampler put the extra samples after one instruction of `ins`:

```
movq 8(%r14), %r8
xchgq %r8, %r14
jmp ...
```

The web makes the edge a swap of the node and its child. `%r14` is the
cursor of the walk down the tree, so the swap is on the chain of loads.
On this processor an `xchgq` of two registers is three micro-ops and cannot
be removed at rename. A move can. The old code had four moves.

Then each cycle went through `%r11`. The park path of `ssa_parallel_moves`
already does this for longer cycles. rbtree and cfold took the same time as
main. nqueens took 5% less. But the instruction counts of rbtree and cfold
then lost most of their gain.

The same change to the edge, without the web, gave deflate_decode +1.1%
and pmap_insert +0.7% in instructions. The noise of this machine hides any
change in time.

## The decision

Neither change lands. The web saves instructions where the edge was a
swap. The swap costs more time than the moves it removes. Where the edge
is not a swap, the web takes a saved register across the other arm of the
branch. Then sort_ints on x86-64 and deriv on arm64 pay for it.

## The unchanged word of the parent

The third case of `balance_left` rebuilds the parent as `Node(Black, ...)`.
On the hot path the parent came through the Black arm, so its first word
does not change. `rejoined_words` makes the word `(old & mask) | 1`, and
the unique path writes it again. To skip the write, the pass must know two
facts. The color byte of `old` is 1, and no field uses bytes 2 and 3. SSA
has neither the layout of the variant nor the two values of `Color`.

A change to the assembly by hand put the rebuild and the write on the path
that allocates only. rbtree at 420,000 then ran 499.0 million instructions
in place of 511.0 million, 2.4% fewer. Its time at 4,200,000 did not change:
the medians of 24 runs were 825 ms before and 860 ms after. The loads of
`ins` and its allocations set its time, not its count of instructions. So
this change waits for a measurement that shows a gain in time.

## Open

- A web that makes the edge a swap can stay apart. Its phi then takes
  the home of its own operands, as before.
- The `xchgq` of `ssa_arg_moves`, before a call, has the same cost. No
  measurement asked for a change there.
