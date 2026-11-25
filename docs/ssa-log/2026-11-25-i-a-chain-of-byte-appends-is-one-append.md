# A chain of byte appends is one append

Part of #15589.

`out.append(b0).append(b1).append(b2).append(b3)` was four appends. Each one
loaded the length, computed the room, compared, stored one byte and wrote the
length back. `ssa.fuse_byte_pushes` now makes one append of up to four bytes
from such a chain. It runs last in `ssaprepare.with_selects`, so both native
backends use it.

A chain qualifies when these conditions are true:

- Each append is an owned append to a `u8[]`.
- The next append of the chain is the only reader of each result.
- All the appends are in one block.
- There is no call between two appends of the chain.
- Nothing between two appends reads the array of the first append.

The fused append takes the place of the last append of its group.

The emitters move the array and the bytes to the five registers that a cold
append changes already: `rdi`, `rsi`, `rdx`, `rcx` and `rax` on x86-64, and
`x0`, `x1`, `x2`, `x3` and `x30` on arm64. Thus the register allocator needs
no new rule. The bytes are packed into one word, low byte first, with `shl`
and `movb` on x86-64 and `bfi` on arm64. One `movl` or `movw` (`str` or
`strh`) stores them, and one add updates the length.

The slow path is one call to `__fern_arr_pushn_owned_u8`. That routine
appends the bytes one at a time through `__fern_arr_push_owned_u8`. So no
operand has to live across a call, and the save masks of a cold call do not
change.

Sometimes the bytes are bytes 0 to n-1 of one value in order, for example
`bb as u8` and `(bb >> 8u64) as u8`. Then the fused append takes that value
as its operand (`ssa.PUSH_PACKED`). The shifts and the wraps then have no use
and are removed. The four-byte flush of `zlib.__compress_block` is this
shape.

## Measured

x86-64 Linux, Callgrind, against main at `9001ecd3`. The program is
`bench/deflate_encode.fern` with `text(4000000)`, one pass, and
`deflate.zlib_compress(input, 1)` in place of `deflate.deflate(input)`. Both
builds return 23.

| Build | Instructions |
| --- | ---: |
| main | 631,967,897 |
| fused bytes, packed by the emitter only | 628,094,033 |
| fused bytes, with the packed value | 621,517,996 (-1.65%) |

`zlib.__compress_block` goes from 87.6M to about 77M instructions.
`bench/deflate_encode.fern` and `bench/deflate_decode.fern` have the same
instruction counts as before, because their chains are not in loops that run
often.

`TestSelfHostArrPushFused` runs chains of 2, 3, 5 and 9 bytes. It also runs a
byte that is read again after its chain, an array with a second holder, and
arrays that grow from empty. It runs on each host target with and without
`FERN_RC_FREE_DEBUG`, and that mode takes the slow path only. The output is
the output of `fern -interp`.
