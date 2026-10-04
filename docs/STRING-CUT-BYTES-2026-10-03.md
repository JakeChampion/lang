# Raw byte input and field selection for cut

`cut` reads chunks, carries incomplete records and selects fields as byte
arrays. Raw delimiter searches and range writes preserve arbitrary input;
long records accumulate in a builder rather than repeated string joins.

The reproduced builder compiler passes 57 shared GNU cases on Darwin and
core WebAssembly. Successful exits have balanced allocation censuses.
Coverage includes every byte, byte and field selection, complements,
delimiters, NUL records, files, stdin and read boundaries. This consumer
does not change the compiler; reproduction is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

Built by the same compiler, raw selection adds 1,760 bytes of code and
416 bytes of unwind data. Static data and the 133,009-byte file size are
unchanged. The added code handles owned raw record buffers and range output.
No size baseline changes.

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
| short bytes | 21.243 (20.563-24.787) | 23.394 (22.748-28.162) | 21.867 (21.431-26.937) | 15.602 (14.840-18.642) |
| short fields | 33.000 (31.991-33.647) | 34.794 (34.144-35.972) | 39.766 (38.860-41.252) | 23.436 (22.918-23.971) |
| long bytes | 11.233 (10.922-11.409) | 9.139 (7.967-10.254) | 5.571 (5.428-6.176) | 5.986 (5.851-6.745) |
| long fields | 37.144 (36.757-37.413) | 34.933 (34.664-35.208) | 47.658 (47.356-50.174) | 27.034 (24.461-28.182) |

Raw times are lower with disjoint observed ranges for long bytes, long fields.
Raw times are higher with disjoint observed ranges for short fields.
The text/raw ranges overlap for short bytes.
These are workload-specific results from this run.

Separate peak RSS samples in bytes:

| Workload | Text | Raw bytes | GNU | uutils |
|---|---:|---:|---:|---:|
| short bytes | 1294336 | 1294336 | 1490944 | 1900544 |
| short fields | 1294336 | 1327104 | 1556480 | 1900544 |
| long bytes | 70385664 | 43188224 | 1490944 | 18743296 |
| long fields | 62095360 | 34848768 | 1507328 | 18808832 |

GNU, uutils and both Fern versions produce identical benchmark outputs.
