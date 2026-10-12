# 2026-10-12 — a child is read where its parent dies

`semsink.fern`, new; `ssasem` guards; `ssaunits.donor_slots`; `ssadeps.Dom`.
Refs #11943.

## The shape

```fern
match (t) {
    Node(Red(), l, n, r) => {
        if (is_leaf(l)) { return Node(Red, l, n + 1, r); }
        return Node(Red, bump(l), n, r);
    },
    ...
}
```

The match reads `t`'s payloads before the colour test, where a failed test
still needs `t` for the next arm, and the arm that rebuilds the node lies
below the colour test and the leaf test. `t` dies in that block, but `l` and
`r` were read above it, so the plan only borrows them: `bump(l)` is handed a
retained `l`, which then has two counts, and the next level finds its node
shared and copies it. Every update copied the whole path.

## The rule

`semsink` re-reads, at the start of a block, each counted projection the
block uses that a block dominating it read, when the projection's parent is
not live out of the block. A value never changes, so the re-read is the same
value. Read where the parent dies, it is a payload the plan takes, nulling
the slot when the parent is unique, and `bump(l)` receives `l`'s one unit.
A block's uses include the phi operands its edges hand on: tail recursion
modulo cons turns `bump` into a loop, and the child goes to the next
iteration through a phi. A projection whose parent was read above too is
re-read from the parent's re-read, so a nested pattern's inner payloads come
from a child that is itself taken.

A re-read must not hold the parent longer than the plan already has to, so
it is made only where the first read borrowed and nothing spends the parent
while the child lives:

- The parent lived on past the first read's block, or can be re-read in the
  block too. A read whose parent died with it took the child already;
  re-reading it held the parent to the later block, which cost a record
  update its box (`update-keeps-no-field-held-twice`) and turned up #12189,
  a string-view payload held and never released.
- No block the child is live out of uses the parent other than to read a
  projection of it or test its variant. A record read before a loop and
  moved into it is spent there; re-reading its field after the loop kept the
  record alive through the loop, and every step copied it
  (`field-read-outlives-the-record-move`, 13 allocations to 52).

Two checks had to learn that a re-read is the same value:

- `ssasem`'s guard check settled a variant only for the value the test
  named, so a payload read of the re-read child was "unguarded". It now gives
  each value a twin, one value standing for every read of the same
  projection of the same box, and a test settles every twin. The reads a
  block rewrites name its re-reads, so a child can have a lower id than its
  parent, and a twin settles its parent's twin first.
- `ssaunits.donor_slots` found the read proving a dying enum's variant only
  in the construction's block and its sole predecessors. It now records each
  value's payload reads once per function and asks the dominator tree, so a
  read above an inlined `is_leaf`'s join still counts. `ssadeps.Dom` carries
  the immediate dominators for the walk.

## Measured

`TestX86_64ProjectionReread` (and arm64, wasm), a 20-node left spine bumped
at its leftmost node, census balanced throughout:

| | main | this change |
|---|--:|--:|
| unique tree, 100 rounds | 2,030 | 30 |
| unique tree, 400 rounds | 8,030 | 30 |
| old tree kept, 400 rounds | 8,030 | 8,030 |

The rbtree benchmark at 420,000 inserts, x86-64:

| | main | previous entry | this change |
|---|--:|--:|--:|
| rbtree allocations | 11,726,271 | 11,044,153 | 4,270,564 |
| rbtree-ck allocations | 11,390,986 | 10,635,012 | 5,761,707 |

The compiler compiles itself in the same time (23.3 s against 23.4 s for a
stage 2 on the 4-core container), and a sanitizer stage 1 compiles
`checker.fern` clean. Compiling `checker.fern` allocates 0.4% more
(`scripts/selfhost-alloc-bench`, 23.77 M to 23.86 M): the liveness and
dominators semsink computes for a function where a block uses a projection
another block read. It rebuilt every block of such a function at first, which
cost 1.5%; it now rebuilds only a block that re-reads.

Code grows where an arm gains a reuse path: `.text` +27% on perceus_rbtree,
+17% on rbtree_ck, +6% on cfold and +4% on deriv, x86-64, with rbtree
retiring 14% fewer instructions and rbtree_ck 11% fewer.

## What is left

A rotation such as rbtree's `balance_left` builds three nodes from two
dying boxes. Both are donors now, but a block holds one token at a time, so
the parent dies while the child's token waits for its construction and is
released: each rotation still allocates twice. Holding a pending token in
the donor's own local, as a carry does across blocks, would let both serve.
