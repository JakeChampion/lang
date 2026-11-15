# A load moves to the side that reads it

2026-11-15. All native targets and wasm: the pass is in `ssa.form`. Follows
`2026-11-15-c-a-store-takes-an-immediate.md`.

## The shape

A `match` loads each binding of a pattern before it tests the nested
patterns. For `Node(_, Node(Red(), lx, kx, vx, rx), ky, vy, ry)` the code
loaded seven fields and then tested the colour of the inner node. When the
test failed, the next arm loaded its own bindings and used none of the
first seven. Most of those values had no register, so each load also had a
store to the frame.

On rbtree 420,000, the insert function ran 12 such instructions on each of
3.2 million returns of the recursive call.

## The change

`ssa.sink_loads` runs at the end of `ssa.form`. It moves an `LD_AT` from the
end of a block to one successor of the branch. The conditions:

- Each instruction after the load in its block reads only: a load, a
  constant, a unary or a binary operation, or the uniqueness test
  (`pure_tail`). Thus the memory that the load reads is the same at the top
  of the successor.
- No instruction of the block and no phi reads the result.
- The successor has one edge into it, and it dominates each block that reads
  the result (`sole_reader`).

A round moves each such load, and the rounds stop when no load moves. A load
whose base is another moved load moves one round later, and keeps its place
before its reader. The limit is eight rounds.

## Measured

Cachegrind instruction counts, x86-64, with the small inputs:

| workload | before | after | change |
| --- | ---: | ---: | ---: |
| rbtree 420,000 | 604,841,163 | 574,315,108 | -5.0% |
| rbtree-ck 420,000 | 970,881,875 | 940,355,820 | -3.1% |
| deriv 8 | 112,155,504 | 112,155,504 | 0 |
| cfold 16 | 38,525,789 | 38,525,789 | 0 |
| nqueens 10 | 27,745,452 | 27,432,840 | -1.1% |

Koka runs 593,463,215 instructions on rbtree 420,000. Fern now runs fewer.

Of the 40 programs of `bench/`, `perceus_rbtree`, `perceus_rbtree_ck` and
`perceus_nqueens` run fewer instructions, and the text of each other program
does not change its count. Each of the 626 programs of `tests/stdlib` gives
the same output. Stage 2 and stage 3 of the compiler are the same file.

## Traps

- `map_string` is not a measure of a change of this size. Its count changes
  by 0.5% from one run to the next, because the map takes a random seed.
- A value that a phi reads does not move. The phi reads it at the end of the
  block before, not in the block of the phi.
- A successor with two edges into it can run when the block of the load did
  not. The pass does not move a load there.

What stays open:

- The pass moves only loads. A constant or an operation that one side reads
  stays where it is.
- The result of the recursive call has its home in the frame. The code
  loads it to a scratch register again before each of five reads.
