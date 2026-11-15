# A shared next node is tested again

2026-11-15. All targets. The change is in the drop helper of a recursive
enum (`ssarc.drop_variant_chain`) and in the certifier
(`rccertify.regions_round`).

## The leak

`2026-11-15-b-a-drop-reads-the-next-node-first.md` made the helper test
the count of the next node before it drops the other fields of the node.
That entry says that a next node with one count after that drop is
released "with the usual release, which frees it". That is not correct.
The release of an enum box is `__fern_rc_dec`. It frees the box at zero,
but it does not drop the fields of the box.

Let a node hold the same node in two fields. The test sees two counts.
The drop of the other field lowers the count to one. Then
`__fern_rc_dec` frees the next node, and its fields stay live:

```fern
enum Tree { Leaf, Node(Tree, string, Tree) }
let shared = Node(Leaf, label(i), Leaf);
let t = Node(shared, label(i + 10), shared);
```

Three such trees in an array leaked three strings: 96 bytes on x86-64
and arm64, 72 bytes on wasm32.

The compiler has this shape. Compiled with the helper of that entry, the
compiler leaked 68 blocks when it compiled `compiler/checker.fern`
(`x86_64/checker.allocs` was 68 more than `x86_64/checker.frees`). The
first leaked block that `FERN_RC_TRACE` found was a string of the parser.

## The change

When the first test says that the next node is shared, the helper tests
it again after the other fields drop. The helper keeps the result in the
word of the first test. A next node that is unique now goes round the
loop, and the loop drops its fields. A next node that is still shared
gets `__fern_rc_dec`, which cannot free it.

A release of a shared next node through `__sem_release_<T>` is also
correct. But it made rbtree-ck 4% slower, by CPU time. The test again
keeps the decrement inline.

## The certifier

The lifted SSA has two edges into the block that goes round the loop:
one from the branch on the first test, and one from the branch on the
second test. Each edge proves that the next node is unique. The
certifier opened a unique region only at a block with one predecessor,
so it refused the helper.

`regions_round` now also opens a region at a block with more edges. Each
edge into the block must leave a branch whose taken arm tests the same
box (`each_edge_tests`). Two cases of `TestSelfHostRcCertifyCases` hold the
rule: `unique-child-two-tested-edges` is accepted, and
`unique-child-one-untested-edge`, where one edge tests nothing, is
refused.

## Measured

x86-64, callgrind, against main 696306db:

| | before | after |
|---|---|---|
| perceus_rbtree | 26,049,411 | 26,049,411 |
| perceus_rbtree_ck | 36,502,101 | 36,946,581 (+1.22%) |
| perceus_deriv | 64,642,318 | 65,549,518 (+1.40%) |
| perceus_nqueens | 73,602,052 | 74,450,452 (+1.15%) |
| perceus_cfold | 45,393,314 | 45,527,714 (+0.30%) |

The second test runs only on a shared next node. rbtree-ck at 4,200,000
takes the same CPU time: 1.76 s before and 1.77 s after, best of 9. When the
compiler compiles `checker.fern`, it now frees each block that it
allocates.

## Tests

- `TestSelfHostDropChainFreesASharedNextNode` runs the shape above with
  `FERN_LEAKCHECK` on the three targets. It fails before the change.
- `conformance/cases/a_drop_frees_a_node_that_both_fields_hold`, with a
  leak census row of 0.

## The trap

`TestSelfHostDropReadsTheNextNodeFirst` already built a node with one
subtree in both fields, but it checked only the exit status. `main`
dropped that node through inline code, so the helper never met the
shape. A test of a drop helper must reach the helper and count the
frees.
