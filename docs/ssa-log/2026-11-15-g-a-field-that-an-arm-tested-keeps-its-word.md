# A field that an arm tested keeps its word

2026-11-15. The first change is on the native targets, where the narrow
fields of a variant share a word. The second change is on all targets.

## The shape

`ins` of rbtree matches `Node(Red(), l, kx, vx, r)` and returns
`Node(Red, ins(l, k, v), kx, vx, r)`. The colour, the flag and the key share
one word. The key and the flag come from the old node at the same place, so
the constructor kept their bits. It did not know that the colour was the
same, so it built the word again and stored it:

```
movq %r12, %rdi
andq $-256, %rdi
movq %rdi, %r10
...
movq %r10, (%r15)
```

The node is updated in place on this path, so the store wrote the value that
the word had. The path runs one time for each level of each insert.

## A tested field

The arm of a match that tests a value for a variant now records the test
while its body is open (`semsource.State.tested`). `kept_bits` asks
`tested_place` about a field that is a constant. The field keeps its place
when an open arm tested the value at that place for the same variant.

When each narrow field keeps its place, the new word is the old word. The
constructor passes the old word, and the in-place path has no store for it.

The rule is about values, not about memory. The word is a value of the
graph, and the arm proves which bits it has.

## A load that both sides read

After the first change the word had no reader on the in-place path. Its load
stayed at the top of the loop, because the cold paths of the two arms read
it. `ssa.sink_loads` moved a load to one side of a branch only when the
other side did not read it.

`split_target` adds the second case. Each side of the branch gets a copy of
the load when all of these conditions are true:

- Each side has one edge into it.
- Each reader of the load runs after one of the two sides.
- One side, or the two, does not read the load in its first block.

The blocks that run after the second side read the copy
(`renamed_sides`). Each copy can move again in the next round. In `ins` the
copies stop in the blocks that allocate a new node.

## Measured

Instructions at the small sizes, x86-64:

| | before | a tested field | and the split load |
|---|---|---|---|
| rbtree 420,000 | 611.0 M | 598.2 M | 591.4 M |
| rbtree-ck 420,000 | 942.0 M | | 915.9 M |
| deriv 8 | 103.3 M | | 103.3 M |
| cfold 16 | 33.77 M | | 33.77 M |
| nqueens 10 | 23.90 M | | 23.90 M |

Koka runs 593 M instructions for rbtree at this size.

## Tests

`TestSelfHostTestedFieldKeepsItsWord` checks the emitted code of two
functions. The function that keeps the colour does not build the word, and
it loads the word after the test of the count. The function that changes the
colour builds the word. The program runs each function on nodes with one
count and with two counts, on each target.
