# A drop keeps its stack in the nodes it frees

2026-11-16. All targets. The change is in the chain drop of a recursive enum
(`ssarc.drop_variant_stack`). It continues
`2026-11-15-d-a-drop-does-not-call-itself-for-a-childless-box.md`.

## The shape

The chain drop walks the last field of its own type in a loop. For each
other field of that type, it called itself for a child with one count. A
tree of rbtree-ck has 22 levels on one side, so the release made 22 nested
calls. The return predictor of the processor holds about 16 return
addresses, so it mispredicted most of the other returns
(`2026-11-15-c-a-drop-makes-one-call-for-each-level.md`). The free of Koka
has no call for a level. It keeps the way back in the nodes that it frees.

## The change

A variant whose last field and one other field are of the type of the chain
has a waiting field (`waiting_field`). When its child has one count, the
drop does not call itself. The node becomes a cell of a stack in place:

- The waiting field keeps the child.
- The last field keeps the cell below it, after the drop reads the next
  node from it.

The loop then goes on to the next node. When the chain ends, the drop takes
the cell on top of the stack (`popped`). The child of the cell is the next
node, and the cell below it is the new top. The drop frees the cell, unless
the cell is the node of the call, which its caller frees.

A child with no field that holds a count is freed at once, and it does not
go on the stack.

## The certifier

The certifier accepts a load from a box as a unit of the frame in an arm
that proves the box unique. A cell and its child each have one count. The
drop tests each of them with `__fern_rc_is_unique` before it reads the
fields of the cell, so the certifier sees the loads as units. When a test
fails, the drop releases the box with the release helper, which releases
what the box holds. That path does not run, but it keeps the count of each
unit correct.

## Measured

Processor time, x86-64, best of 7 runs:

| Workload | Before | After |
|---|---:|---:|
| rbtree-ck 4,200,000 | 1930 ms | 1804 ms (-6.5%) |
| deriv 10 | 1031 ms | 967 ms (-6.2%) |
| cfold 20 | 143 ms | 142 ms |

The loop runs more instructions than the calls did. Cachegrind counts
rbtree-ck 400,000 at +4.0%, deriv 8 at +3.4% and cfold 16 at +0.7%. The
instruction count does not see the cost of a mispredicted return, so the
time decides (`scripts/perf-bench`). rbtree and nqueens do not change.

## Tests

- `TestSelfHostDropKeepsAStackInItsNodes` checks that the drop of `Tree` has
  no call of a drop helper. The trees share subtrees, one node holds one
  subtree twice, and one left side has 200,000 nodes. The census is balanced
  on x86-64, arm64 and wasm32.
- `TestSelfHostDropReadsTheNextNodeFirst` checks the order of the read of
  the next node and a call only when the drop has a call.
- `TestSelfHostDropSkipsAChildlessBox` gives `Pair` three fields of its type,
  so its drop still has the call and the test of the shape before it.
