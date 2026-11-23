# A word load has its offset in its address

Part of #15976 and #16159.

## The shape

The read of two, four or eight bytes of an array is one bounds check and one
load. The address of the load was a sum that the lift made in two adds:
`(base + 8) + index`. Each backend emitted the two adds and then a load
through the result. The read of one byte had the address in the load already.

`ssa.fold_byte_access` gave a byte load at `add(a, b)` the address `a + b`.
It is now `ssa.fold_sum_access`, and the instruction `LD_BYTE` is now
`LD_SUM`: a load of `a + b + imm` with an element width. The pass takes these
loads when the add is in the block of the load and nothing else reads the sum:

- a byte load or a byte store, as before
- the unary loads `load_u16` and `load_i32`
- an `LD_AT` of a byte, of 32 bits, or of a word that an integer instruction reads

The pass then moves a constant that one side of the sum adds into `imm`. The
constant must be a multiple of 8, and the total must be 4088 or less. Each
backend has such an offset in one load of each width.

On x86-64 the load is one instruction, `movzwl 8(%rax,%r12), %esi`. On arm64
no load has two registers and an offset, so the load is `add` and then a load
at `#8`. That is one instruction less than before.

A word load that only a return or a phi reads keeps its two adds. Such a value
can get a float register, and `LD_SUM` has no float form.

An `LD_AT` whose one reader is a compare also keeps its form. x86-64 reads
such a load in the compare, so the load needs no register. The compare has no
form with two registers in its address. That form is the next step.

## The allocator

Two rules of the allocator did not know the instruction, and each one cost a
register at each load:

- The result of an add or of a unary load can take the register of an operand
  that has its last read there. The result of `LD_SUM` now can too.
- On x86-64 a value can have its home in `%rcx`, `%rdx` or `%r11`, except
  where an instruction uses that register for a value from a slot. `LD_SUM`
  denied all three. It uses `%r11`, and `%rcx` when its index is not a
  constant, as a checked element read does.

Without these two rules and the compare rule, the lookup of `core/map` put one
more value in a slot. `map_probe_chain.dr` went up 9.5%.

## The loop split

The change removes values from the inner loops of `zlib.longest`. With fewer
values there, the allocator accepted a split around the inner loop for four
values. Three of them have one read in each step of the chain loop, after the
inner loop. Each split put a load in the block of that read, so each step of
the chain loop got three loads of two instructions.

A split is now a plan only when its store and its load weigh less than the
reads of the value in the loop (`ssa.split_cost`). A value with one read in
the block of the load does not pay. `longest` keeps the one split whose load
is on the cold side. `TestSelfHostLoopSplitKeepsOnceReadValues` pins this,
and `deflate.__codes` keeps the split of `TestSelfHostLoopSplit`.

## Measured

x86-64, callgrind, a program that calls `zlib_compress` at level 6 on 2 MB of
text from a 16-letter alphabet. The base is main at `974b8d53`.

| Build | Instructions |
| --- | ---: |
| main | 647,340,349 |
| the fold alone | 670,800,620 |
| the fold and the split rule | 619,391,231 |
| all of this change | 620,572,703 |

A step of the chain loop of `longest` has 20 instructions, and it had 22.

`scripts/perf-bench` on x86-64, against the pinned rows. No row is slower by
more than its tolerance.

| Row | Pinned | Measured | Change |
| --- | ---: | ---: | ---: |
| `map_int.ir` | 24,115,264 | 23,651,802 | -1.92% |
| `map_probe_chain.ir` | 235,291,505 | 230,890,651 | -1.87% |
| `map_string.ir` | 49,328,509 | 48,024,349 | -2.64% |
| `video_qtrle.dr` | 321,555,951 | 299,830,951 | -6.76% |
| `deflate_decode.ir` | 26,942,125 | 26,737,578 | -0.76% |
| `deflate_encode.ir` | 41,015,265 | 41,015,509 | +0.00% |

`map_probe_chain.dr` is 43,194,356, as on main. `deflate_encode` does not call
`longest`.

The compiler that this change builds compiles `compiler/fern_main.fern` to a
binary that is byte for byte itself.
