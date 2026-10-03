# Raw byte pieces and patterns for csplit

`csplit` carries input records and output pieces as bytes. Byte BRE matching
finds split points without treating arbitrary input as UTF-8. Partial
records accumulate in a builder, and streamed tails use byte range writes.

The reproduced builder compiler passes 52 GNU cases on each of Darwin
and core WebAssembly, comparing exit status, byte counts, diagnostics and
every output file. Cases include all bytes, numeric and regex splits,
captures, offsets, repetitions, suppressed and empty pieces, Unicode output
names, retained and removed error pieces, long records and read boundaries
through files and pipes. Successful exits have balanced allocation censuses;
abrupt errors retain live allocations.
Compiler reproduction is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

The parent-source comparison exposes live allocations on successful exits:
48 bytes on Darwin and 56 on core WebAssembly. The candidate's successful
exits are balanced. Missing-match errors retain 2,792 bytes on Darwin,
down from 2,840, and 2,184 on core WebAssembly, down from 2,232. Empty-input
errors retain 1,640 and 1,296 bytes respectively, down from 1,688 and 1,352.
This migration does not establish cleanup on abrupt exits.

Built by the same compiler, raw records, matching and range output add
2,632 bytes of code and 448 bytes of unwind data. Static data and the
248,769-byte file size are unchanged. No baseline changes.

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
| numbered | 8388603 | 10.197 (7.870-12.981) | 8.596 (7.691-13.853) | 39.572 (38.614-50.995) | Output differs |
| regex | 8388600 | 28.017 (15.178-30.542) | 27.792 (15.415-31.233) | 68.262 (64.199-81.094) | Output differs |
| long line | 8388615 | 54.701 (49.476-68.417) | 13.407 (10.043-27.435) | 14.057 (12.898-18.189) | Output differs |
| streaming tail | 8388614 | 13.888 (8.436-28.002) | 12.165 (6.369-19.601) | 18.777 (14.894-21.241) | Output differs |

Raw times are lower with disjoint observed ranges for long line.
The text/raw ranges overlap for numbered, regex, streaming tail.
These are workload-specific results from this run.

Separate peak RSS samples in bytes:

| Workload | Text | Raw bytes | GNU | uutils |
|---|---:|---:|---:|---:|
| numbered | 2113536 | 2228224 | 1261568 | - |
| regex | 2080768 | 2146304 | 1343488 | - |
| long line | 119865344 | 43384832 | 39157760 | - |
| streaming tail | 1900544 | 1818624 | 55984128 | - |

GNU and both Fern versions match stdout, exit status and every output
file. uutils rejects the malformed UTF-8 in all four workloads in both
the pilot and full run, exits 1 and produces no files. Those runs are
excluded from timing comparison.
