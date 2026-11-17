# A wide node waits on the drop stack

2026-11-17. All targets. The change is in the chain drop of a recursive enum
(`ssarc.drop_variant_chain`). It continues
`2026-11-16-h-a-drop-loop-keeps-no-flag.md`. Issue #16172.

## The shape

A variant with one waiting field of its own type and a last field of that
type waits on a stack that the nodes hold. A variant with two or more waiting
fields did not: `waiting_field` gave no field, and the drop called itself for
each such child with one count. A tree of `Node(Tri, Tri, i64, Tri)` thus made
one call for each level, and a deep tree used one frame of the machine stack
for each level.

## The change

`waiting_fields` gives each waiting field of a variant. A node with two or
more of them goes on the stack with each child in place
(`chain_many_children`). Its last field keeps the cell below, and its last
child is the next node. The node is not freed there.

The pop of such a cell reads the waiting fields from the last to the first
(`cell_keeps`). A field that is not equal to the link holds a child. The pop
takes that child, writes a copy of the link into the field, and leaves the
cell on top of the stack. When only the first waiting field is left, the cell
leaves the stack as a cell with one child does.

The copy of the link is the mark of a taken child. A child is never equal to
the link: each cell of the stack has one count that the stack holds, and the
bottom link is 0.

The certifier needs no change. The two reads of the comparison are not
counted loads, so they make no unit. The read of the child is a counted load
in the arm that proved the cell unique.

## A trap: one stack for each waiting field

The first design had one stack for each count of waiting children. A cell
with two waiting children was on stack 2, and it went on stack 1 when one
child left. No field needed a mark.

That drop was correct, and it ran 6% fewer instructions than the calls. It
made the program slower. The two stacks do not keep the order of one stack.
The nodes were thus not freed in the order of a walk of the tree. The free
lists then gave the next tree blocks that were far apart. The program built 6
trees of 13 levels. The build of a tree after the first took 18 to 25 ms,
against 11 ms with the calls. The time of the drop itself did not change.

The instruction count does not see this cost. Measure the time of a program
that builds a tree after a drop.

## Measured

The program builds and frees 20 full trees of `Node(Tri, Tri, i64, Tri)` with
13 levels. x86-64, 11 runs of each binary in turn, user time:

| | Before | After |
|---|---:|---:|
| Best | 627 ms | 536 ms |
| Median | 676 ms | 558 ms |
| Instructions (Cachegrind) | 3,009,949,944 | 2,857,603,964 |

The best run gains 14.5%, and the instruction count falls 5.1%. The build of
each tree after the first takes 11 to 13 ms before and after.

The code of an enum with at most one waiting field in each variant does not
change.

## Tests

- `TestSelfHostDropOfAWideNodeHasNoCall` checks that the drop of an enum with
  variants of two and three waiting fields has no call of a drop helper, on
  x86-64 and arm64. Each field of each variant is in turn the spine of a tree
  of 1,000,000 levels. One frame for each level needs more than a stack of
  8 MB. The trees share subtrees. The census is balanced on x86-64, arm64 and
  wasm32.
- `TestSelfHostDropSkipsAChildlessBox` now checks that the drop of `E`, whose
  `Pair` has two waiting fields, has no call. Its new enum `G` keeps its next
  nodes in an array. The drop of `G` thus still has the call and the test of
  the shape before it.

## What stays

A variant that keeps its next nodes in an array (`chain_tail_array`) still
calls the drop for each element but the last. It also calls the drop for a
field of its own type before the array. A field can hold the type of the chain
in another type, for example in an option or a record. Such a field also has a
call.
