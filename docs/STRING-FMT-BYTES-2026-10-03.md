# Raw byte paragraphs for fmt

`fmt` stores lines and paragraph word ranges as bytes. Partial records
accumulate in a byte builder, and completed lines enter the existing
paragraph layout algorithm without unchecked text construction.

The reproduced builder compiler passes 44 GNU cases on each of Darwin
and core WebAssembly, with balanced allocation censuses. The corpus covers
every byte, formatting modes, prefixes, control characters, long words,
paragraphs and read boundaries through both files and pipes. Compiler
reproduction is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

Built by the same compiler, raw paragraphs add 1,176 bytes of code and
320 bytes of unwind data. Static data is unchanged. The additional raw
buffer and range operations cross a Mach-O text-segment page boundary:
the segment grows from 131,072 to 147,456 bytes. The signature grows from
1,297 to 1,425 bytes, with 41 hashes instead of 37. These measured changes
explain the file growth from 149,393 to 165,905 bytes. No baseline changes.

Linux GNU byte and ordinary option corpora, primary target checks, the
full serial unit suite and `make lint-all` pass. Source files match the
frozen validation snapshot.

Native arm64 Darwin measurements use the reproduced builder compiler,
GNU 9.12 and uutils 0.12.0 in the C locale. A pilot at `--size 8192`
precedes the same pipeline at `--size 8388608`; actual input sizes are
listed below. Exact output is checked before two warmups and seven
alternating samples. RSS is sampled separately. Other compiler and
container jobs remain idle during measurement.

Times are median milliseconds with observed minimum and maximum.

| Workload | Input bytes | Text | Raw bytes | GNU | uutils |
|---|---:|---|---|---|---|
| paragraphs | 8388608 | 118.772 (117.358-121.777) | 121.360 (120.891-123.340) | 59.825 (59.505-62.316) | 175.875 (174.978-181.696) |
| uniform | 8388608 | 100.515 (98.652-101.916) | 102.986 (100.958-130.022) | 51.448 (50.897-53.363) | 145.700 (144.960-146.170) |
| prefix | 8388608 | 115.732 (114.803-119.567) | 118.244 (116.998-126.683) | 59.282 (57.896-63.671) | Output differs |
| long line | 8388619 | 54.702 (53.932-56.944) | 11.236 (10.469-11.635) | 13.030 (12.755-13.538) | 25.819 (25.468-27.124) |

Raw times are lower with disjoint observed ranges for long line.
The text/raw ranges overlap for paragraphs, uniform, prefix.
These are workload-specific results from this run.

Separate peak RSS samples in bytes:

| Workload | Text | Raw bytes | GNU | uutils |
|---|---:|---:|---:|---:|
| paragraphs | 1458176 | 1409024 | 1228800 | 1884160 |
| uniform | 1490944 | 1409024 | 1228800 | 1835008 |
| prefix | 1458176 | 1376256 | 1228800 | - |
| long line | 132366336 | 51757056 | 1228800 | 18726912 |

uutils differs on the prefix workload at output byte 14 in both the pilot
and full run. Output lengths agree: 8,739 and 8,947,849 bytes respectively.
Those two runs are excluded from the timing comparison.
