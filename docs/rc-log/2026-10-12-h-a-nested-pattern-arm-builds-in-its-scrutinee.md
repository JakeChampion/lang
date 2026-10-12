# 2026-10-12 — a nested pattern's arm builds in its scrutinee

Fixes #11943. `ssaunits.donor_slots`.

## The shape

```fern
match (t) {
    Node(Red(), l, _, r) => { return Node(Red, l, n, r); },
    Node(Black(), l, _, r) => { return Node(Black, l, n, r); },
    Leaf => { return Node(Red, Leaf, n, Leaf); }
}
```

The match reads `t`'s payloads before the colour test, because a failed test
still needs `t` for the arms after it. The arm that rebuilds the node is the
test's successor, and `t` dies at its construction there. A flat
`Node(c, l, _, r)` reads and builds in one block, and reuses `t`'s box.

`Tree`'s variants have different slot counts, so a dying `Tree` has a count
only where a payload read proves its variant. `donor_slots` looked for that
read in the construction's own block, found none, and the nested arm
allocated on every update.

## The rule

`donor_slots` also looks in the chain of sole predecessors above the block.
Every path into the block runs through those blocks, and a value's variant
cannot change, so a payload read there proves the variant in the block.
`at` gives the blocks' positions by id, so the walk is a lookup per block.

## Measured

`TestX86_64NestedPatternReuse`, with the arm64 and wasm legs alongside: a
tree with live children, updated through both colours, so the Black arm
always follows a failed Red test. Allocations under the leak census:

| | main | this change |
|---|--:|--:|
| unique input, 100 rounds | 104 | 4 |
| unique input, 400 rounds | 404 | 4 |
| shared input (old tree kept), 400 rounds | 404 | 404 |

The shared rounds still copy, one node per update, and each old tree keeps
its value and children.

The rbtree benchmark, 420,000 inserts, x86-64, census balanced in every run:

| | main | this change |
|---|--:|--:|
| rbtree allocations | 11,726,271 | 11,044,153 |
| rbtree-ck allocations | 11,390,986 | 10,635,012 |

## What is left

`ins` now builds every node on the path in its own box; only the new leaf
allocates. The rest of rbtree's allocations, about 14 per insert, are in
`balance_left` and `balance_right`. Each rotation builds three nodes from the
parent and its red child, and only the parent is a donor. The child is read
in the block that tests its colour, where the parent is still needed, so it
is only borrowed and never owned. Taking it from a parent the frame owns and
finds unique would make it the second donor.
