# A hole starts at a cell of the frame

2026-11-16. x86-64 and arm64. The change is a pass of the SSA backend,
`ssaholecell.cells`, and the instruction `ssa.FRAME_CELL`. The pass runs
after the certifier.

## The shape

A cons-tail loop builds its result from the front. Each turn fills the hole
of the turn before it with a new box. The first fill has no hole: its box is
the result. So each fill tested that the hole is null:

```
cmpq $0, %r13
jne  store
movq %rsi, %r12        the first box is the result
jmp  next
store:
movq %rsi, 8(%r13)
```

After `2026-11-16-j-a-second-test-of-a-token-is-decided.md`, this test was
most of the tests against zero that were left: 5.9% of the instructions of
cfold and 3.4% of rbtree.

## The change

`ssa.FRAME_CELL` gives the address of a cell of one word in the frame, less
an offset. The cell has a slot of the frame, as a view that lives in the
frame has (`ssa.view_slots`). x86-64 emits `leaq` from `%rbp`, and arm64
emits `sub` from x29. A function with a cell has a full frame.

`ssaholecell.cells` looks for the phi of a hole:

- An edge into its block gives it a null.
- Each use of it is the null test of a fill or the store of a fill.
- The result of each fill goes only to the phis that carry the result, and
  to a return.

The pass then changes the loop:

- The hole starts at the address of the cell, less the offset of the store.
  The first fill thus writes the cell.
- Each fill is the store, with no test. The two sides of the test go away.
- The phis that carried the result go away. A return reads the cell.
- A return can give the result before a fill. The cell then starts null.
  No workload of the bench corpus has such a return.

The hole can be a box with a fixed offset, or the address that
`ssaholeaddr.addresses` makes for a loop with two hole positions.

## Measured

Cachegrind on x86-64, the bench corpus, against the build with the decided
tests:

| row | before | after | change |
|---|---:|---:|---:|
| perceus_rbtree | 24,285,754 | 23,459,834 | -3.4% |
| perceus_cfold | 38,428,137 | 37,302,537 | -2.9% |
| perceus_rbtree_ck | 31,331,455 | 30,870,495 | -1.5% |
| perceus_deriv | 51,571,941 | 51,571,941 | 0.0% |
| perceus_nqueens | 55,024,075 | 55,024,075 | 0.0% |

One more program adds 1 to each cell of a list of 1,000 cells, 100 times. It
runs 2,148,444 instructions in place of 2,348,644. The loop loses the test,
the branch, and the register of the result.

`TestSelfHostSSAHoleCell` runs the pass on loops made by hand.
`TestSelfHostHoleStartsAtAFrameCell` has a loop with one hole, a loop with
two hole positions, and boxes with one count and with two counts. The census
is balanced with the sanitizer on three targets. The compiler rebuilds
itself byte for byte.

## Open

- Each new box still gets a null in the field of its hole, and the next fill
  writes that field again.
- `ins` of rbtree moves 11.5% of its instructions to and from the stack. The
  register allocator keeps a value in its frame slot for its whole life when
  one call is in that life.
