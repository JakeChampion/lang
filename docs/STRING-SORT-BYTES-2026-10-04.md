# Sort byte input and comparisons

Sort keeps input records, merge buffers, random seeds and diagnostic excerpts
as bytes. Malformed UTF-8 and NUL bytes survive sorting, checking and merging.
Filenames read from `--files0-from` are validated as complete UTF-8 records
before reaching filesystem APIs.

Long-double and version comparisons share a borrowed input interface. Text
callers keep their existing entry points; Sort retains array/range wrappers
across comparisons. Random comparisons borrow one retained seed view. This
avoids creating view objects for every comparison. The `-R` ordering still
takes precedence when combined with `-V`.

The primary interpreter now implements the 32- and 64-bit leading-zero,
trailing-zero and population-count operations used by numeric comparisons.
A separate regression compares all six operations against Go's integer
oracle on 133 values, including every bit position, complements and zero.
Darwin's closed-stdout diagnostic also preserves GNU's close error after a
large failed write.

## Validation

The integrated source includes main `f4de66fd5`. Full Linux units and all
lint gates pass. GNU parity, raw-input fixtures, comparator fixtures and
primary tests pass on Linux x86-64/ARM64, Darwin ARM64 and WASM as supported.
Shared consumers cover Stat, LS, Dir, Vdir, Printf, Seq, Numfmt and Od.
The raw corpus checks file and stdin input, NUL records, invalid UTF-8,
numeric/version/random modes, keys, merging, checking and diagnostics.
Native and core-WASM runs under the reproduced compiler have balanced
allocation censuses. Comparator execution also passes in its interpreter.

All three Darwin bootstrap stages are identical at 13,029,377 bytes, SHA-256
`046ae956a04fd1246653f97fbf575b9d3e27742fb70458f2270ec3dba9b69add`.
The actual compiler-output corpus passes in 6.303 seconds.

## Measurements

Both versions use that reproduced compiler and the same standard library.
The before sources are Sort at `b69c31acd` and `ld`/`vercmp` at `b9503762d`;
those three files are identical to main `f4de66fd5`. The after build changes
those modules and adds the byte-input adapters. Task-owned heavy jobs were
idle during timing; the desktop was not isolated.

A 1,024-record pilot passes before scaling only the record count to 65,536.
Input order is `(i * 7919) % records`. Workloads use zero-padded integers
with malformed UTF-8 suffixes, signed decimal records, version-like names,
already ordered input, alternating records in two merge files, and a
16-byte zero seed for random ordering. `LC_ALL=C` is fixed. Each workload
checks exact output, status and diagnostics against GNU 9.12, followed by
allocation checks, two warmups and seven alternating timing samples.

Median elapsed time for 65,536 records:

| Mode | Before | After | GNU 9.12 | uutils 0.12 |
| --- | ---: | ---: | ---: | ---: |
| Raw | 6.812 ms | 7.009 ms | 13.821 ms | 6.725 ms |
| Numeric | 8.344 ms | 8.498 ms | 24.116 ms | 12.615 ms |
| General numeric | 654.660 ms | 660.137 ms | 43.714 ms | 14.591 ms |
| Version | 117.904 ms | 97.746 ms | 74.416 ms | 22.582 ms |
| Check | 2.643 ms | 2.543 ms | 4.774 ms | 4.127 ms |
| Merge | 4.137 ms | 4.217 ms | 6.986 ms | 4.722 ms |
| Random | 1091.531 ms | 1087.494 ms | 170.857 ms | Different ordering |

Version-sort ranges are disjoint: 116.599-120.268 ms before and
96.486-102.950 ms after. Every other before/after timing range overlaps.
General numeric and random sorting remain substantially slower than GNU;
this migration does not resolve that existing performance gap. uutils
matches every measured result except random ordering, which is recorded as
incompatible and excluded from timing comparisons for that workload.

Every candidate census balances. The baseline retains 786,432 bytes in raw
and random sorting, 731,448 bytes in numeric/general sorting, and 1,168,544
bytes in version sorting. Both versions balance in check and merge modes.
Allocation counts change as follows:

| Mode | Before | After |
| --- | ---: | ---: |
| Raw | 618 | 621 |
| Numeric | 637 | 640 |
| General numeric | 51,652,886 | 51,652,889 |
| Version | 807 | 828 |
| Check | 126 | 138 |
| Merge | 564 | 563 |
| Random | 17,381,972 | 17,381,974 |

The Darwin executable grows from 430,897 to 430,913 bytes. Text grows from
357,688 to 362,764 bytes, unwind data from 26,796 to 28,332 bytes, and data
from 17,032 to 17,288 bytes. The byte-input, borrowed-comparison and cleanup
paths fit within the existing executable segments. No size or performance
baseline changes are needed.
