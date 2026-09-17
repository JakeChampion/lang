# The freelist that stopped

`-backend ssa` on arm64 reported hundreds of KB live at exit where the stack
machine reported hundreds of bytes. #9542 reverted the arm64 default over it.
Four readings of that number were published and retracted before the fifth one
held. The bug is real, it is not a leak, and it is not in the accounting.

## What it is

`__alloc` and `__free` both derive a block's freelist **class** from the byte
count they are handed. So the size a string is allocated at and the size it is
freed at have to be the same number. They were not:

- most producers allocated `len + 9` — the rc word, the length word, and the
  trailing NUL;
- `__fern_str_dec` freed `len + 8`;
- four producers (concat, `strbuf_take`, the slice, bytes-to-string) allocated
  `len + 8` themselves and wrote no NUL.

At most lengths `round16(len + 8)` and `round16(len + 9)` are the same class and
the disagreement cancels, which is how it survived. Exactly where `len + 9`
lands ON a class boundary they differ by one class. The block is then pushed
onto a class nothing ever requests: the freelist stops handing anything back,
every allocation bumps the cursor, and the heap grows without bound while the
program's live set is constant.

A `read_line` loop over 8,000 lines, one line width per row:

| L | bumped | popped | pops | live_bytes |
| ---: | ---: | ---: | ---: | ---: |
| 22 | 256,096 | 255,968 | 7,999 | 32 |
| **23** | 640,064 | **0** | **0** | **128,032** |
| 24 | 256,160 | 383,904 | 7,998 | 32 |
| **39** | 768,064 | **0** | **0** | **128,032** |
| 40 | 256,192 | 511,872 | 7,998 | 32 |

`pops = 0`. Not reduced — zero. 16,002 blocks allocated and none reused.

## The fix

One constant, `strBlockBytes = 9`, used by every string producer, by the free,
and by the in-place growth check. The four `len + 8` producers move up to it,
because a free path that sees only `len` cannot tell the two shapes apart, and a
block returned at a LARGER size than it was allocated is worse than a leak — it
lands on a class whose later request gets a block too small for it.

The growth check was the third site and the one I missed: `__fern_str_append`
decides whether the grown string still fits its class by computing
`round16(total + 8)` against the class extent of `la + 8`. Left alone, it
believed a 32-byte block held 24 bytes of string where it now holds 23, and
would have written one byte past the block at exactly class capacity. The
backend's own append tests caught it, which is the argument for the tests
carrying explicit sizes: three of them encode the layout in their fixtures, and
they fail loudly when it changes rather than drifting quietly.

After: every boundary width reads 32, every control is unchanged, and the four
random-width bands that first exposed this collapse from 6,432 / 6,272 / 11,488
/ 8,256 to 32.

## How the four wrong readings happened

1. *"The single-word string ABI's reclaim taint."* Refuted by a control: x86-64
   runs the same ABI with the same taint and is clean. The fix for that taint
   (#9549, a real and separate bug) changed these programs not at all —
   byte-identical binaries — which is what exposed the misattribution.
2. *"One retained buffer whose size tracks the input."* Refuted by holding the
   line count fixed: uniform width reports the same 32 B at 1,000 lines and at
   8,000.
3. *"The census over-reports when allocation sizes vary."* Refuted twice —
   alternating 40/56 spans two classes and stays clean, so variety is not the
   trigger; and the census was accurate all along.
4. *"`__free` tallies the size class while the bump side does not."* Refuted by
   reading `emitFreelistClass`: below 2048 bytes it rounds to exactly 16, the
   same as the bump path.

Every one of those was consistent with the aggregate numbers available at the
time. What separated the fifth from the other four was not more care — it was
a **prediction made before it was believed**. The random-band data implied one
specific boundary width per band (widths 1–20 contain exactly one, and the
shortfall was 402×16 against the 400 lines `8000/20` puts there). That predicted
uniform input at the boundary must drift and its neighbours must not, which is a
statement the data could have falsified. It did not.

The general lesson, written down because it cost four rounds: a mechanism that
explains the numbers is not evidence for that mechanism. Only something that
would come out differently under the alternatives is. `pops = 0` is that kind
of number; `live_bytes = 369,040` is not.

## What this does NOT fix

`coreutils/uniq.fern` is **unchanged** at 369,040 bytes over 8,000 lines
against the stack machine's 368. The string-block fix is complete and verified
for strings, and the coreutils retention is a separate instance — most likely
the same class of disagreement in another block kind, since `uniq` holds an
array of lines. So the retention question behind #9542 is still open, and the
next step is to run the same `pops`-based instrument over an array workload.

The test pins widths measured rather than derived: my arithmetic for which
widths sit on a boundary was off by one, and the mutation run caught it by
failing the case I had labelled the control. The bug makes the arithmetic
untrustworthy, so the widths come from observation.
