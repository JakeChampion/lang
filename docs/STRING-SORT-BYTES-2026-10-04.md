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

The integrated source includes main `9c8eb0032` and the prepared byte-Xattr
dependency `3153a7a13`. Full Linux units and all
lint gates pass. GNU parity, raw-input fixtures, comparator fixtures and
primary tests pass on Linux x86-64/ARM64, Darwin ARM64 and WASM as supported.
Shared consumers cover Stat, LS, Dir, Vdir, Printf, Seq, Numfmt and Od.
The raw corpus checks file and stdin input, NUL records, invalid UTF-8,
numeric/version/random modes, keys, merging, checking and diagnostics.
Native and core-WASM runs under the reproduced compiler have balanced
allocation censuses. Comparator execution also passes in its interpreter.

Both bootstraps use the published `stage0-20261004-ef49ae0` pin. All three
Linux ARM64 stages are identical at 12,989,312 bytes, SHA-256
`09de2c7a89319732f2224fe40d481ee07e525df55e1bfd025aad474e83dcf556`.
All three Darwin stages are identical at 13,195,057 bytes, SHA-256
`6d9f79e41f0e89d1ca226fac7d0134631882cbe64c665bd59feddd279a9f7413`.
The actual compiler-output corpus passes in 7.106 seconds. Registry,
admission, Xattr, byte-scan and task-scheduler integration checks also pass.

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
| Raw | 6.548 ms | 6.598 ms | 13.542 ms | 6.354 ms |
| Numeric | 7.901 ms | 7.981 ms | 23.176 ms | 11.982 ms |
| General numeric | 657.753 ms | 670.866 ms | 43.906 ms | 14.625 ms |
| Version | 120.836 ms | 97.950 ms | 74.433 ms | 23.136 ms |
| Check | 2.864 ms | 2.769 ms | 4.936 ms | 4.398 ms |
| Merge | 4.842 ms | 4.639 ms | 9.769 ms | 5.264 ms |
| Random | 1113.396 ms | 1111.689 ms | 174.292 ms | Different ordering |

Version-sort ranges are disjoint: 118.707-123.126 ms before and
97.030-100.890 ms after. Every other before/after timing range overlaps.
Merge timings were noisy across all implementations: 4.157-17.665 ms before
and 4.175-29.782 ms after, so they establish no speed improvement.
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
