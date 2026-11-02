# 2026-11-02 — a count on a constant null goes

`ssa.drop_null_counts`, `ssa.register_form`, `FERN_SSA_ALLOC`. Follows the
`rand_choose` lead of `2026-11-02-a-a-quiet-cell-read-borrows-its-cell.md`.

## The shape

`rand_choose` in `coreutils/shuf.fern` runs once per line `shuf` writes.
Its hot return path stores three fields into the `Rand` box it owns. Then
it calls `__fern_rc_dec` on a value the lift shows as `const_int 0`,
defined at entry.

The lowering gave a reuse token a frame slot and cleared it at entry.
Only the refill path fills the slot. On the hot path the exit releases the
cleared slot: a load, a floor test and a branch, on every call.

The compiler's own text has 442 such sites. One is `lsprefs.target_at`,
where an inlined callee returns `None` from one arm and a call result from
another. The join owns its phi, so the lowering retains the `None` operand
on its edge: a constant null, materialised, floor tested and skipped.

## The rule

A retain or a release on a constant null does nothing. The runtime entries
return at the floor test. `drop_null_counts` runs at the end of
`register_form`. It removes an `__fern_rc_inc` or `__fern_rc_dec` call whose
one operand is a constant zero, when nothing reads the call's result. A
count on any other value stays.

The pass scans for a zero constant and a
count call before it allocates anything. A function without both costs one
pass over its instructions.

## Measured

x86-64, `-O`, instructions from callgrind. `shuf` reads
`--random-source` from one file in both runs, and writes the same bytes.

| | before | after |
|---|--:|--:|
| `shuf -i 1-1000000` | 347,313,932 | 341,313,932 |

The utilities that write a line at a time are unchanged to the instruction,
except `cat -n`, which loses 6.

`fern.fern` compiled by itself, by a stage 1 with and without the pass:

| | before | after |
|---|--:|--:|
| stage-2 compiler, bytes | 15,747,792 | 15,752,144 |
| lines of x86-64 assembly for `fern.fern` | 4,558,739 | 4,556,453 |
| stage 2 compiling `lexer.fern` to x86-64 asm, Ir | 1,713,542,275 | 1,715,539,052 |

The pass itself is the 4 KB the binary grows by. The emitted assembly of
`lexer.fern` is the same, and the pass costs 0.1% of the compile.

The certifier census of `shuf` is the same before and after: 18 unproved
functions, no findings.

## Measurement trap

Two runs of one stage-2 compiler on `lexer.fern` differed by 30% in
instructions, 1.71 G against 2.23 G. Other valgrind jobs and a test sweep
ran on the same four cores at the time. Alone, six runs agree to the instruction.
The compiler has no threads, so the cause is open. Measure compile cost on
a quiet machine, and under `setarch x86_64 -R`.

## Tests

`TestSelfHostSSARegisterForm` runs the register-form driver. Its new case
builds a function with four counts. One is taken on a constant null, and
one is given back on it. One is given back on a parameter, and one is on
the null, with its result read. The first two go, the last two stay.

## A lead that did not pay

The loop-carried values of `rand_choose` live in frame slots. The refill
path runs once per 64 KB of random bytes. It holds its values across soft
calls, and they take the five callee-saved registers by spill weight. The
`FERN_SSA_ALLOC` dump shows the weights.

Two heuristics were measured and reverted. The first weighs a read in a
block the loop takes on some rounds only at one eighth of the loop's
weight: `shuf` -3.7%, but `nl` +1.8%, `cut` +1.1%, and the compiler's own
compile +1.25%. The second makes a loop nested in such a block cold with
it: the stage-2 compiler +2.0% in size.

The refill path's values hold the registers the loop values want for the
whole loop. The shape needs a split of the loop values' ranges around the
refill path.
