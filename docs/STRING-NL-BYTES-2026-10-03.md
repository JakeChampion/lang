# Raw byte records and patterns for nl

`nl` numbers raw records, compares byte delimiters and calls the byte BRE
matcher. Line numbers, padding and separators remain text. Partial records
accumulate in a byte builder across reads.

The reproduced builder compiler passes 58 shared GNU cases on Darwin and
core WebAssembly, comparing status, output and diagnostics. Cases cover
every byte, numbering styles, raw captures, multibyte patterns and page
delimiters, overflow, long lines and read boundaries in file and pipe forms.
Compiler reproduction is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

Successful exits have balanced allocation censuses. Overflow uses the
existing abrupt process exit and leaves live allocations. On the pipe
case, the parent leaves 6,680 bytes on Darwin and 6,216 on core WebAssembly;
the candidate leaves 6,712 and 6,256 respectively. File cases add 24 bytes
to each figure. This migration does not establish cleanup on abrupt exits.

Built by the same compiler, raw records add 2,552 bytes of code and
416 bytes of unwind data. Static data and the 232,273-byte file size are
unchanged. The added code handles raw buffers, delimiters and BRE entry
points. No size baseline changes.

Linux raw-byte and ordinary GNU parity checks, primary target checks,
the full serial unit suite and `make lint-all` pass. Source files match
the frozen validation snapshot.

Native arm64 Darwin measurements use the reproduced builder compiler,
GNU 9.12 and uutils 0.12.0 in the C locale. The 8,192-byte pilot precedes
the same workloads at 8,388,608 bytes; long-line workloads append a newline.
Outputs are checked before two warmups and seven alternating samples.
RSS is sampled separately, with other compiler and container jobs idle.

Times are median milliseconds with observed minimum and maximum.

| Workload | Text | Raw bytes | GNU | uutils |
|---|---|---|---|---|
| plain | 25.504 (24.994-25.899) | 24.512 (23.975-26.485) | 185.416 (182.737-197.099) | 34.307 (34.092-39.395) |
| long line | 69.394 (69.045-73.031) | 8.287 (7.584-8.624) | 13.601 (13.307-13.857) | 6.362 (6.243-6.655) |
| regex | 47.877 (46.810-49.811) | 49.253 (47.787-54.076) | 223.288 (220.401-228.844) | 42.013 (41.522-42.948) |

Raw times are lower with disjoint observed ranges for long line.
The text/raw ranges overlap for plain, regex.
These are workload-specific results from this run.

Separate peak RSS samples in bytes:

| Workload | Text | Raw bytes | GNU | uutils |
|---|---:|---:|---:|---:|
| plain | 2129920 | 2129920 | 1245184 | 2129920 |
| long line | 131137536 | 43417600 | 9781248 | 18989056 |
| regex | 2097152 | 2080768 | 1261568 | 2441216 |

GNU, uutils and both Fern versions produce identical benchmark outputs.
