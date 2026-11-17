# A shared arm takes one from the count

2026-11-17. x86-64 and arm64. Three changes to the releases and the edge
moves that the SSA backend emits.

## The release of a box is inline at each position

The helper release of a record or a variant has the mark 7
(`ir.box_release`). It had an inline test of its count only where the
emitter saved caller registers around the call. At every other position, the emitter called
`__sem_release_T.r`, and the helper did the same tests again. Now
`ssa_call` gives each mark-7 release to `ssa_rc_dec` on x86-64 and on arm64.
A shared box loses one count and a static box is kept, with no call. Only a
unique box calls the helper.

`check` of binarytrees releases a `Nil` child at each leaf. `Nil` is a static
box, so each of these releases is now two tests and no call.
`TestSelfHostSSABoxReleaseIsInline` pins the shape.

A mark-7 release is now also a count site of `ssadeps.boxed_values`. A load
of the value can come first on each path, as the tag test of a match does.
Then the release has no test against the heap floor.

## The shared arm of a donor

A match that takes a donor apart, or rebuilds it, reads its uniqueness once. The unique arm
frees the box. The shared arm released the box through the full ladder,
which read the count again and had a path to the helper. On that arm the
count is above one or the box is static, so the release cannot free the box.

The new mark `ir.shared_dec` (`ssa.SHARED_DEC`) goes on the helper release
`__sem_release_T` of that arm. The release keeps its name, so the credit
checkers and wasm see the release that they know. The x86-64 and arm64
emitters write it inline, as `ssa_rc_prim` writes an increment: the guard, a
test of the sign, and a subtraction. It has no call, so the register
allocator sees no clobber there.

The mark is correct only if the count at the death is the count that the
token read. A release of another value between the read and the death can
make the box unique, and then the decrement leaks it.

`ssaunits.count_holds` makes sure of this. From the read to the death, each
instruction must do one of three things. It reads a field or the variant of
the donor, it changes no count, or it is the rebuild of the keep. A call
that borrows the donor is not a read, because it can release another value.
No step drops another value, reuses another box or ends another row.

In `depth` of `TestSelfHostSoleReleaseShape*`, the shared arm is now this:

```
cmpl $0, -8(%rbx)
js .Lssa_depth_rcshr52
subl $1, -8(%rbx)
```

The `Leaf` arm of `depth` keeps a ladder. It releases a value that no take
read, so the test lets one ladder stay. A value known to be a payload-free
variant is the static box only if every construction of it is a constant.
That is not proved, so the release stays.

## A cycle of edge moves goes through %r11

`ssa_parallel_moves` wrote a cycle of two registers as one `xchgq`. That
is three micro-ops, and the processor cannot remove it at rename. The cycle
now goes through the scratch register `%r11` with three moves. The processor
can rename each move. `ssa_swap_partner`, `ssa_reads_either`, `ssa_closes_cycle` and
`ssa_int_reg` are deleted. The `xchgq` in `ssa_arg_moves` stays.

## Measured

Instructions on x86-64 under cachegrind, against main at `50625de8a8`, at
small sizes. The output of each run is the same as main's.

| workload | change |
|---|---:|
| treemap | -10.5% |
| binarytrees | -7.4% |
| deriv | -2.0% |
| cfold | -1.8% |
| rbtree-ck | -1.2% |
| msort | -1.0% |
| rbtree | -0.5% |
| pmap | -0.3% |
| nqueens | 0.0% |
| qsort | +0.7% |

The qsort row is the swap in the loop of `append`. The `xchgq` was two
instructions with the load, and the moves through `%r11` are four. The
loaded value went to `%rdi`, which is the destination of the other phi.
With another register, the edge needs no cycle. The processor time of qsort
did not change.

Processor time on x86-64, user time, median of 12 to 30 alternating runs:

| workload | main | this change |
|---|---:|---:|
| binarytrees, depth 19 | 3261 ms | 3142 ms (-3.6%) |
| deriv, 9 | 121 ms | 118 ms |
| qsort, 1e6 | 890 ms | 911 ms (noise) |
| treemap, depth 20 | 645 ms | 669 ms (+3.7%) |

treemap loses time, although it runs 10.5% fewer instructions. The cause is
the `Nil` arm of `bump`, which now tests the count of the static box inline
in place of the call to `__sem_release_Tree.r`. The same listing with the
call put back is as fast as main. This processor is a Skylake-derived Xeon,
where a branch that crosses a 32-byte boundary is slow. With the assembler
option `-mbranches-within-32B-boundaries`, the medians are 655 and 658 ms.
So most of the loss is the place of the branches, and the next item below
removes that release.

The compiler that this tree builds, `bin/fern`, grows from 30.81 to 31.54 MB.

## Next

- The register allocator gives a loaded value the register of another
  phi of the same edge, which makes a cycle. In qsort, `append` loses two
  instructions in each iteration when the load goes to a free register.
- A test of its variant can show that a value is a payload-free variant.
  If each construction of that variant is the static box, the release of
  that value is no code. This removes the `Nil` arm release of treemap and
  of binarytrees.
