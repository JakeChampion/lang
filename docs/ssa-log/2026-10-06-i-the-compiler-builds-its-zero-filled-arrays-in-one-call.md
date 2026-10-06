# 2026-10-06 — the compiler builds its zero-filled arrays in one call

`ssa.zeros`, `ssalive.words_of` and `zeroes`, `ssaunits.bits`,
`ssadeps.flags`, `ssalayout.flags`, `sempair.falses`, `semsource.no_flags`,
`wit_decode.wit_falses`, `parser.zero_counts`, `util.bools`, and the fill
loops that open `semfuse.use_counts`, `seminline.call_counts`,
`rebuilt_values` and `chained_rebuilds`, `ownership.lent_out` and
`carried_values`, `semsource.shared_keys`, `ssarc.use_counts` and
`in_place_maps`. Refs #8171; the second half of `2026-10-06-h`. No emitted
byte changes: the compiler before and after this change builds
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern` for x86-64,
byte for byte.

## What changed

Every loop over `compiler/` whose only work on an array was appending n
zeros, n `false`s or n `0i64`s to it empty is one call of `__alloc_i32`,
`__alloc_bool` or `__alloc_i64`: the eleven helpers that were only that
loop are deleted and their callers call the builtin (the two
`flags(n, bit)` helpers were never called with `true`), the fill loops that
open twenty-odd analyses are one call each, and a loop that filled several
arrays at once keeps only the arrays it fills with something else.
`ssa_lift.filled(n, 0)` is `__alloc_i32(n)` too.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from `2026-10-06-h`'s tree, so the pin does
not enter the comparison.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 18.455 G | 18.006 G (−2.43%) |
| `ssalayout.flags`, self | 101.5 M | gone |
| `ssa.zeros`, self | 89.8 M | gone |
| `ssalive.words_of`, self | 74.9 M | gone |
| `ssaunits.bits`, self | 74.8 M | gone |
| `__fern_arr_push`, self | 388.0 M | 343.7 M |
| `ownership.lent_out`, self | 32.5 M | 16.3 M |
| `__fern_arr_push_owned`, self | 101.1 M | 88.9 M |
| `__fern_alloc_u8`, self | 0 | 20.8 M |

The saving is half as much again as the 300 M the profile named: the
loops' own cost was the four functions above, and the pushes behind them,
with their capacity doublings, were another 60 M. `ssalayout.flags` was the
largest because `Loops.members` is an n×n matrix filled a cell at a time.

## What is left

The `-1` fills (`ssadeps.indices` and the sixteen like it, and the `-1`
arrays left in the mixed loops above) are the same loop with a different
constant, 78 M of self cost together before the pushes behind them, and the
few `true` fills (`seed_bits`, the `dirty` work lists) are the same again; a
fill builtin that takes the value would take them all. The byte buffers the
object writers pad with zeros append to a buffer already holding bytes, so
they are not this shape.
