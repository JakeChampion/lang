# A drop loop keeps no flag

2026-11-16. All targets. The change is in the chain drop of a recursive enum
(`ssarc.drop_variant_chain`). It continues
`2026-11-16-a-a-drop-keeps-its-stack-in-the-nodes-it-frees.md`.

## The shape

The drop helper of `Expr` ran 22.9% of the instructions of deriv, with 62
instructions for each node. The helper of `Tree` ran 13.6% of rbtree-ck, and
the helper of `Positions` ran 10.9% of nqueens.

The loop of the helper kept four flags in locals:

- `first`: the node is the node of the call, which the caller frees.
- `unique`: the next node has one count.
- `pushed`: the node went on the stack.
- `next`: the next node, or 0.

Each arm of the variant set the flags. The code after the arms then tested
them. In the machine code, each flag was a value in a register at the join
of the arms. The join moved five registers for each node, and the tests ran
again on values that the arm knew.

## The change

Each path of an arm now decides and leaves. No flag is left.

A node with one child of its type tests that child:

- A child with one count is the next node.
- A shared child gives back its count, and the loop goes to the stack.

A node with two such children tests the first child and then the last child:

- When each has one count and has children, the node goes on the stack. The
  last child is the next node.
- In each other case at most one child is the next node, and the stack does
  not change.

A child with one count and no child is freed at once. This is true for the
first child and for the last child. Before, a last child with no child was
the next node, and its turn of the loop ran the tests of each variant.

The free of the node compares the node with the node of the call, in place
of the flag `first`.

The first child is tested and released before the test of the last child. A
node that holds one box in two fields thus needs no second test.

Some enums keep the two children of each variant that can wait in the same
fields (`one_cell_shape`). The pop of a cell then reads the fields of the
cell with no test of its variant.

The certifier needs two things, and both stay:

- The store of the first child in the cell, before the node goes on the
  stack. The store gives the unit of the child back to the cell.
- The two uniqueness tests at the pop
  (`2026-11-16-a-a-drop-keeps-its-stack-in-the-nodes-it-frees.md`).

`drop_variant_stack`, `pushed_field`, `chain_link` and `drop_tail_array` are
gone. `drop_variant_chain` makes the loop for each chain enum, with or with
no stack. `chain_tail_array` is the arm of a variant whose next nodes are in
an array.

## Measured

Cachegrind on x86-64, the bench corpus, against the build with the borrowed
walks:

| row | before | after | change |
|---|---:|---:|---:|
| perceus_deriv | 57,591,141 | 51,941,541 | -9.8% |
| perceus_rbtree_ck | 34,726,655 | 31,885,855 | -8.2% |
| perceus_nqueens | 59,218,475 | 56,513,675 | -4.6% |
| perceus_cfold | 40,246,537 | 39,487,737 | -1.9% |
| perceus_rbtree | 24,934,874 | 24,934,874 | 0.0% |

The helper of `Expr` runs 8,044,800 instructions in place of 13,161,600. The
census is balanced with the sanitizer for the five rows.

## Open

- A shared child is tested two times: the uniqueness test fails, and the
  release then reads the count again. One read can do both.
- The free of a node calls `__fn___fern_box_free.u`, which reads the size
  from the header. The arm knows the size of its variant.
- Variants with the same fields, such as `Add`, `Mul` and `Pow`, each have
  an arm.
