# A dead value lends its saved register

2026-11-17. x86-64 and arm64. The change is in
`ssa.regalloc_linear_fixed_costs`. It continues `2026-11-04-e`, which lends a
caller-saved register.

## The shape

`ins` of rbtree at 420,000 ran 526.0 million instructions. 51.1 million of
them, 9.7%, moved a value between a register and the frame.

The result of the recursive call lives across the calls of the balance arms,
so it needs a saved register. x86-64 has five, and each one held a value at
the call. Two of those values, the key and the left child, are dead after
the call on that path. But an interval has no holes, and the two intervals
cover the loop. Thus the result went to a frame slot, and the path loaded it
five times for each level.

## The rule

A value v can be without a caller register and without a free saved
register. The scan then looks at each active value in a saved register. v
takes that register when v and the values in the register do not interfere.
This is the test of the caller lend. The saved lend has other limits:

- The interval of v can hold calls, because a call keeps a saved register.
- A phi, and a value that a phi reads, can lend.
- A value that a phi reads can borrow, but a phi cannot.
- An operand of the definition of v can lend when the emitter already lets
  the result share its home (`def_a`, `def_b`).
- The values of the register hold it to the end of v or longer.
- No mate of a value of the register starts in the interval of v
  (`chain_wanted`).

The copies of an edge are on their own stub, and they are one parallel move.
Thus a phi is safe as a lender.

With the fifth limit, the lend keeps the register from no later value. Of
the registers that qualify, v takes the one that comes free first.

A mate is a value that names another value as its phi mate, or whose
definition reads it. A mate can take the register only while no other value
holds it. Without the last limit, a loop of `pmap.node_find` copied a phi to
its mate on each iteration.

The caller lend keeps its own limits.

## A parameter is live from the entry

The entry of a function writes each parameter. The `param` instruction of a
parameter can come later in the first block. `live_near` took the position
of that instruction as the definition. Take a value with its definition
before that instruction. The value was not live at the definition of the
parameter. The parameter was not live at the definition of the value.

A second test hid this in most cases.

With the lend from parameters, the compiler compiled its own
`arm64_native.arm64_rrr` incorrectly. The mask of `base` was in the register
of `w`. The select read the mask in the place of `w`. Each `umulh` of an
arm64 build then had a wrong word. The compiler still reproduced itself, so
`make distcheck` passed. The arm64 run of the workloads found it.

`live_near` now takes the entry as the definition of a parameter, and
`live_at_def` gives the answer directly: only a parameter is live at the
definition of a parameter.

## The cost of a chain with a borrower

`chain_weight` and `chain_len` counted the first value of a chain without a
test. For a chain of mates, the first value is the one that holds the
register. A borrower is not a mate, and it can end before its lender. The
chain of a cold parameter with an old borrower then had the weight of the
two values. In `pmap.node_insert`, a child node that the hot path reads five
times lost its register to that chain.

A borrower that ended now counts no more (`heads`), and an eviction does not
move it to the frame. A mate counts as before: the hole pointer of `ins` and
its mates are one value of the loop.

## Measured

Instructions on x86-64, from `scripts/perf-bench` on main and on this tree:

| row | main | this tree | change |
|---|---:|---:|---:|
| perceus_rbtree | 23,459,827 | 22,847,987 | -2.6% |
| deflate_decode | 27,654,925 | 26,950,522 | -2.5% |
| perceus_rbtree_ck | 30,870,437 | 30,224,997 | -2.1% |
| pvec_with | 46,694,212 | 46,375,485 | -0.7% |
| gather_many | 105,118,047 | 104,726,815 | -0.4% |
| ordmap_insert | 68,668,262 | 68,420,002 | -0.4% |
| perceus_deriv | 51,514,334 | 51,439,934 | -0.1% |
| pmap_insert | 49,737,377 | 49,732,338 | 0.0% |
| sort_strings | 93,478,334 | 93,478,334 | 0.0% |

`ins` of rbtree at 420,000 now has 29.6 million moves between a register and
the frame. The run has 511.0 million instructions.

The emitted text of a benchmark changes in the two directions. The largest
change is 0.8% on x86-64 and 1.4% on arm64.

## Tests

- `TestSelfHostSSASavedRegisterLends` requires that no recursive call of
  `rbtree.ins` stores its result to the frame.
- `TestSelfHostSSAParameterIsLiveFromTheEntry` runs a function of the shape
  of `arm64_rrr` on each target. It fails on x86-64 without the change to
  `live_near`.

## Open

- `ins` still keeps three values in the frame on its hot path: the packed
  word of the node, the packed word of the result, and the parameter `v`.
  The first two can be read again from their node after the call.
- A phi cannot borrow. The tests of a lend are the same for a phi, but no
  measurement asked for it.
