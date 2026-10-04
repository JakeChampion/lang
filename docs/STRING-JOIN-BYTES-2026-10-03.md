# Raw byte records and diagnostics for join

`join` stores record buffers, keys and disorder diagnostics as bytes.
Records spanning multiple reads accumulate in a builder. Byte comparisons,
range writes and a raw GNU diagnostic helper preserve arbitrary input
without constructing text; diagnostics retain stdout flush ordering.

The reproduced builder compiler passes 18 GNU cases on each of Darwin
and core WebAssembly, comparing status, output and diagnostics. Cases
cover every byte in keys, duplicate keys, case folding, headers, unpaired
fields, strict and deferred disorder, stdin, unterminated records and
long keys and values across read boundaries. Successful exits and the
measured deferred-disorder exits have balanced allocation censuses.
Strict disorder retains the existing abrupt exit. On Darwin, its live-byte
count changes from 5,664 to 5,680; core WebAssembly remains at 5,472. This
migration does not establish cleanup on abrupt exits. Compiler reproduction
is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

Built by the same compiler, raw record and diagnostic operations add
1,904 bytes of code and 368 bytes of unwind data. Static data and the
166,033-byte file size are unchanged. No baseline changes.

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
| short records | 16777200 | 70.939 (69.930-72.572) | 82.039 (79.643-83.121) | 144.601 (143.770-161.525) | 118.977 (117.342-120.403) |
| long key | 16777229 | 96.225 (95.413-97.900) | 14.146 (13.892-14.420) | 127.903 (126.650-131.194) | 10.020 (9.760-11.914) |
| long value | 8388632 | 52.721 (52.006-54.431) | 9.354 (9.155-18.651) | 66.658 (65.486-69.842) | 6.832 (6.086-7.614) |

Raw times are lower with disjoint observed ranges for long key, long value.
Raw times are higher with disjoint observed ranges for short records.
These are workload-specific results from this run.

Separate peak RSS samples in bytes:

| Workload | Text | Raw bytes | GNU | uutils |
|---|---:|---:|---:|---:|
| short records | 1687552 | 1720320 | 1196032 | 2048000 |
| long key | 139608064 | 43368448 | 32620544 | 35733504 |
| long value | 131186688 | 34963456 | 16941056 | 18956288 |

GNU, uutils and both Fern versions produce identical benchmark outputs.
