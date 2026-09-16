# Measured 2026-09-16: spilling a call-crossing value over saving it

`2026-09-16-call-site-pad.md` left 63,974 push and pop pairs around calls
in the x86-64 SSA build of the self-hosted driver: values live across a
call, homed in caller-saved registers because the five callee-saved ones
were taken, saved and restored at every call in their range. A function
like `parse_stmt_at` has hundreds of such values and thousands of calls.

**What changed.** `LinearScan` counts, per value, the calls it is live
across and its uses, and `allocateLinear` gives a call-crossing value a
spill slot rather than a caller-saved register when the calls outnumber
the uses and the definition by a factor, on a target that distinguishes
the two register classes. A slot costs a store at the definition and a
reload at each use; a caller-saved register costs a push and a pop at
each call crossed.

**The factor is measured, not derived.** The first draft compared
instruction counts (spill when two saves a call exceed one reload a use)
and made the driver 70 KB larger; a byte model (a push and pop of two to
four bytes against a reload of six or seven) suggested a factor of two,
which is better but not best. The binary keeps shrinking as the factor
rises past what either model predicts, then grows again, so the rule is
picked from the sweep. On the main of #9455 (`1a98e7015`), the driver
built with `fern -target x86-64-linux -backend ssa`:

| spill when calls exceed | binary | text segment |
| --- | --- | --- |
| never (main) | 9,090,811 B | 8,772,786 B |
| 2 × (uses + 1) | 9,000,699 B | 8,682,354 B |
| 6 × (uses + 1) | 8,935,163 B | 8,615,858 B |
| 8 × (uses + 1) | 8,914,683 B | 8,594,098 B |
| 12 × (uses + 1) | 8,926,971 B | 8,606,002 B |
| 16 × (uses + 1) | 8,926,971 B | 8,608,146 B |
| 24 × (uses + 1) | 9,025,275 B | 8,706,482 B |

The likely reason the models undershoot: a value that crosses many calls
has a long interval, and a register it does not occupy is a register the
values around it are not spilled for, so the saving compounds past its
own pushes. The factor of eight is the constant `spillOverSaveFactor`.

Against the flat backend's 8,795,528 B the SSA driver is now 1.4% larger,
from 6.5% this morning.

**Checked.** The driver reads its source on stdin (`asm_ir_run -ir` with
the module piped in; a path argument is ignored, and a check that passes
one compares the empty program). With `lexer.fern` on stdin the driver
built with the rule writes the same 392,454 bytes as the flat-built
driver; `parser.fern`, `checker.fern` and `irlower.fern` bail identically
(in 1.85 s, 1.09 s and 8.55 s against flat's 2.10 s, 1.19 s and 9.80 s).
Best of five on the call-heavy benchmarks, the rule against main:

| bench | main | with the rule |
| --- | --- | --- |
| `call_overhead` | 4.26 ms | 3.93 ms |
| `closure_call` | 21.8 ms | 22.0 ms |
| `enum_match` | 11.9 ms | 11.7 ms |
| `tokenize` | 13.1 ms | 12.7 ms |
| `map_string` | 15.2 ms | 14.6 ms |
| `struct_drop` | 65.6 ms | 65.7 ms |
| `ordmap_insert` | 47.1 ms | 47.0 ms |
