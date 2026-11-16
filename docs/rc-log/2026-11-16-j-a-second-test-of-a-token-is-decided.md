# A second test of a token is decided

2026-11-16. x86-64 and arm64. The change is a pass of the SSA backend,
`ssasame.decided`. It runs after the certifier.

## The shape

A reuse token is a box or null. The lowering tests it for each thing that it
decides:

- a count for a field that the function takes
- the allocation
- the words that the box already has
- the release of the box that was shared

`ins` of rbtree tested one token four times on one path. One test was in the
arm of the same test. Another test was at the join of the two arms of the
same test.

A test of a value against zero ran 7.3% of the instructions of rbtree and
6.2% of cfold.

## The change

`ssasame.decided` looks for a branch on `eq` or `ne` of a value and zero.
Each of its two targets has that branch as its one predecessor. A second
branch that tests the same value at the same width is decided in two shapes:

- The second branch ends a block that one target of the first branch
  dominates. It becomes a jump. A reuse allocation of the value on the zero
  side takes a constant zero, so `ssa.fresh_boxes` still knows that it is
  fresh.
- The second branch ends the join of the two sides. The join holds only phis
  and the test. One target of the branch only jumps to the other target.
  Each side then goes to its own target, and the phis of the join move to
  the second target.

The pass copies no block. A function with no value that two branches test
is not changed and costs one read of its branches.

## Measured

Cachegrind on x86-64, the bench corpus, against the build with the direct
free of a box that a match takes apart:

| row | before | after | change |
|---|---:|---:|---:|
| perceus_rbtree | 24,647,034 | 24,285,754 | -1.5% |
| perceus_cfold | 38,773,737 | 38,428,137 | -0.9% |
| perceus_rbtree_ck | 31,539,775 | 31,331,455 | -0.7% |
| perceus_deriv | 51,571,941 | 51,571,941 | 0.0% |
| perceus_nqueens | 55,024,075 | 55,024,075 | 0.0% |

`ins` of rbtree has 39 tests of a value against zero in place of 47. The
census is balanced with the sanitizer for the five rows. The compiler
rebuilds itself byte for byte. `TestSelfHostSSASameTests` runs the pass on
graphs made by hand.

## Open

- The other tests of a token are one test for each path, or they are far
  from the test above them: other branches are between the two. To decide
  them, each side needs its own copy of those blocks.
- A fill of the hole of a cons-tail loop tests that the hole is null. That
  test is most of what is left: 5.9% of the instructions of cfold and 3.4%
  of rbtree. A hole that starts at the address of a frame slot for the
  result needs no test.
