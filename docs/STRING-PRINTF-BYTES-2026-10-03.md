# Raw byte conversions and diagnostics for printf

`printf` emits escapes, byte characters, precision-truncated strings and
raw diagnostics directly. Unicode literals and numeric conversions retain
their existing behavior, without using text to carry arbitrary bytes.

Direct checks with the reproduced builder compiler pass on Darwin and
core WebAssembly. Cases cover every escaped byte, character conversions,
format reuse, early termination, precision and padding across scalar
boundaries, warnings and invalid-conversion diagnostics. Successful exits
have balanced allocation censuses; abrupt errors are recorded below.
Compiler reproduction is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

Built by the same compiler, raw conversions add 912 bytes of code and
232 bytes of unwind data. Static data is unchanged. The file grows from
215,537 to 215,553 bytes: Mach-O rebase metadata grows from 728 to 744 bytes,
while the signature remains 1,809 bytes. The added code handles byte
conversions and range output. No size baseline changes.

Native measurements use 16 repeats for the pilot and 128 for
the full run, with only the repeat count changed. Both runs verify output
before timing. Two warmups precede seven alternating samples; peak RSS
comes from a separate run. No other compiler or container job was running.

GNU coreutils 9.12 is the output oracle; Rust uutils 0.12.0 is also
checked. Timings include only implementations with identical stdout,
stderr and exit status.

| Workload | Implementation | Median ms | Range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| escaped | text | 2.787 | 2.549-3.497 | 1,310,720 |
| escaped | bytes | 2.482 | 2.302-3.395 | 1,310,720 |
| escaped | gnu | 3.559 | 3.482-3.698 | 1,294,336 |
| escaped | uutils | 2.815 | 2.596-3.523 | 2,555,904 |
| literal | text | 1.667 | 1.626-1.849 | 1,245,184 |
| literal | bytes | 1.672 | 1.601-1.798 | 1,212,416 |
| literal | gnu | 3.291 | 3.240-3.467 | 1,179,648 |
| literal | uutils | 2.171 | 2.117-2.275 | 1,933,312 |
| precision_numeric | text | 1.781 | 1.662-1.830 | 1,228,800 |
| precision_numeric | bytes | 1.697 | 1.658-1.791 | 1,228,800 |
| precision_numeric | gnu | 3.352 | 3.221-3.471 | 1,163,264 |
| precision_numeric | uutils | 2.176 | 2.046-2.329 | 2,097,152 |

All before/after timing ranges overlap. These measurements establish
the observed cost without claiming a speed improvement.

| Workload | Argument bytes | Output bytes |
| --- | ---: | ---: |
| escaped | 131,073 | 32,768 |
| literal | 18,433 | 18,432 |
| precision_numeric | 4,225 | 1,664 |

The parent-source census fails successful conversions, including 176
live bytes on Darwin and 120 on core WebAssembly for ordinary escaped
output. The candidate balances all successful cases. Invalid Unicode
conversion exits abruptly: Darwin live bytes fall from 728 to 664.
Core WebAssembly falls from 560 to 512 bytes. This is not an error-exit cleanup
guarantee.

Linux passes the new byte tests, primary target checks, existing GNU and
primary parity corpora, the full unit suite and all lint checks. The BRE
regression gate includes expr, tac, nl and csplit. The local GNU printf
oracle was rebuilt against glibc 2.39 because its older glibc 2.36 build
rejected binary prefixes already required by the corpus. No test expectation
or production numeric behavior changed to accommodate that environment.

The new Darwin byte tests pass. Its existing numeric corpus retains the
same failing cases as the unchanged parent, already covered by the
repository's Darwin CI exception list. They include binary prefixes, NaN
spelling, alternate-form rounding and oversized directives. No exception
was added or broadened; this change does not claim full Darwin printf parity.
