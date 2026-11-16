# A function makes its frame where it calls

2026-11-16. x86-64 and arm64. The change is in the emitters, with the
analysis in `ssa.frame_split`.

## The shape

`count` of deriv ran 27.5% of the instructions of deriv 8. Half of its calls
get a leaf, and return 1. Each call pushed three registers at the entry and
restored them at the return. Only the arms that call `count` again use those
registers. A leaf call ran 11 instructions, and 6 of them were the
frame.

A function such as `fib` has the same shape. Its guard returns before a call.
But its parameter lives across the calls, so the entry moved it to a saved
register.

## The change

`ssa.frame_split` splits a function whose frame only saves registers:

- An early block has only moves, loads, constants and integer operations that
  cannot trap. No value of the block has a home that the frame saves. Each
  predecessor of an early block is early.
- A site is a block that is not early and has an early predecessor. Each
  predecessor of a site is early. The emitter makes the frame at the start of
  a site.
- A block that the emitter copies to the end of another block, for a rotated
  loop, has the same side as that block.
- The split is empty unless an early block returns.

The edges are the edges of the emitter. A jump to a block that only passes
the jump on goes to the end of that chain (`ssa.forward_target`). The label
of that block is not on the path.

A parameter whose home the frame saves is a late parameter (`EarlyHomes`). In
the early blocks it stays in the register of its argument. Each site moves it
to its home after the saves. The split is empty when another parameter has
that register as its home, and no early block writes it.

The CFI rows follow the text. The emitter remembers the state of the entry.
An early block that follows text with a frame restores that state. A block
with a frame that follows early text states the rows of the frame.

x86-64 takes a function with a bare frame. arm64 takes a function with no
slot, no canary and no profile.

## Measured

Cachegrind on x86-64, against the build with the direct free:

| program | before | after | change |
|---|---:|---:|---:|
| fib 32 | 95,166,103 | 66,968,759 | -29.6% |
| deriv 8 | 95,903,544 | 91,716,342 | -4.4% |
| cfold 16 | 30,578,293 | 30,047,791 | -1.7% |
| nqueens 10 | 22,218,662 | 22,218,656 | 0.0% |
| rbtree 420,000 | 556,682,328 | 556,682,322 | 0.0% |
| rbtree-ck 420,000 | 830,568,600 | 830,568,594 | 0.0% |

The answers on x86-64, and on arm64 under qemu, are the same.

The compiler does not gain from the split. A compile of
`tests/stdlib/quic_conn_test.fern` runs 34,597,541,569 instructions before
and 34,568,990,091 after, which is 0.08% fewer.

## Traps

The first version read the edges from the terminators. In
`tests/stdlib/quic_conn_test.fern` a site was a block that only passes a jump
on. The emitter sent the jump past its label. One path then ran with no frame
and returned to a wrong address. `ssa_frame_split_run` has that graph.

The chain of a jump depends on the homes, because a move on an edge stops it.
The early blocks have other homes for a late parameter. The split is empty
when the two sets of homes give different chains.

## Open

- A site must have only early predecessors. A loop that follows a guard has a
  header with an early predecessor and a back edge, so that function keeps
  its frame at the entry. A block on the edge can make the frame there.
- An early block has no call. A tail call from an early block needs no
  frame, and it is not taken yet.
- A debug build with variable rows keeps each parameter in one home, so it
  takes no late parameter.
