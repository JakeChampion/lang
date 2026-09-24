# Loop-invariant interval extension skips values no loop reaches

`ssa.extend_loop_invariant_intervals` stretches a value's interval to a
loop's back edge when the value is defined before the loop header and still
live at it. It walked every value for every back edge, and found each
edge's header by scanning every block. In a stage-2 profile of compiling
`lexer.fern` that inner value loop ran about 1.1 M times, 30 M
instructions in all.

Each value's extension depends only on the back edges, taken in block order,
so the values are independent. A value can only extend when some back-edge
header lies after its definition and no later than its current end, and a
binary search over the sorted header positions rules out most values. The
values that pass still go through the edges in the original order, so every
interval comes out the same. Headers are found through a table indexed by
block id.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 (self-host-built) compiler, Ir | 1,766,063,154 | 1,738,818,288 (−1.5%) |

The stage-2 compilers' output for `lexer.fern` is byte-identical, and so is
the assembly the native-built compilers emit for `checker.fern` on x86-64
and arm64.
