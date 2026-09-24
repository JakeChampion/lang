# The array release is inline where the count survives it

`__fern_rc_dec`, the release of a counted box, was a call to
`__fern_arr_dec` at every site. In a stage-2 compile of `lexer.fern` it ran
8.5 M times at about 15 instructions each, and most of those did not free:
the box was null, static, or shared. Only about 2.7 M boxes are allocated.

Both register backends now emit the release inline, as #10093 did for the
retain: below the heap floor (every null) or with a static box's negative
count nothing happens, and a count above one is decremented in memory. A count
of one, the free, or zero, the underflow the helper reports, still calls
`__fern_arr_dec`. With the sanitizer or the use-after-free quarantine on, the
release stays a call, so the helper's poison and underflow checks run.

| | main | this change |
|---|---|---|
| `lexer.fern` compile, stage-2 compiler, Ir | 1,804,527,667 | 1,755,714,960 (−2.7%) |
| `checker.fern` compile, stage-2 compiler, wall clock (mean of 4) | 9.83 s | 9.21 s (−6.3%) |
| stage-2 compiler binary | 9,754,248 B | 11,201,784 B (+14.8%) |

Both stage-2 compilers are built from this change's source, one by main's
compiler and one by this one; the assembly they emit for `lexer.fern` is
byte-identical. Built with leakcheck and compiling `checker.fern`, both make
and free 100,723,253 allocations with no live bytes left.

The size is the cost: about 25 bytes at each of the self-host compiler's
release sites. The driver-size gate is unaffected, since it links drivers
the native compiler builds.
