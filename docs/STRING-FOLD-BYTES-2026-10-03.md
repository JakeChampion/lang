# Raw byte input and control scans for fold

`fold` carries input and incomplete records as byte arrays. A single
membership scan finds control bytes for the selected folding mode, and
range writes preserve raw data through column and byte-width folding.

The reproduced builder compiler passes 72 shared GNU cases on Darwin and
core WebAssembly, with balanced allocation censuses. Cases cover every
byte, byte and column widths, space folding, control characters, files,
stdin and read boundaries. This consumer does not change the compiler;
reproduction is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

Built by the same compiler, raw folding adds 764 bytes of code and
328 bytes of unwind data. Static data and the 116,273-byte file size are
unchanged. The added code handles owned raw buffers and membership scans.
No size baseline changes.

Linux raw-byte and ordinary GNU parity checks, primary target checks,
the full serial unit suite and `make lint-all` pass. Source files match
the frozen validation snapshot.

Native arm64 Darwin measurements use the reproduced builder compiler,
GNU 9.12 and uutils 0.12.0 in the C locale. The 8,192-byte pilot precedes
the same workloads at 1,048,576 bytes; long-line workloads append a newline.
Outputs are checked before two warmups and seven alternating samples.
RSS is sampled separately, with other compiler and container jobs idle.

Times are median milliseconds with observed minimum and maximum.

| Workload | Text | Raw bytes | GNU | uutils |
|---|---|---|---|---|
| short columns | 777.482 (749.654-782.350) | 5.832 (5.483-11.034) | 13.527 (13.444-13.970) | 4.708 (4.530-5.551) |
| short spaces | 777.371 (754.782-780.676) | 6.209 (5.339-32.395) | 13.708 (13.480-14.232) | 4.788 (4.532-5.199) |
| long columns | 6890.743 (6882.468-6955.816) | 6.430 (4.897-7.884) | 12.306 (11.808-13.838) | 5.172 (4.788-10.911) |
| long bytes | 2.911 (2.721-3.599) | 2.747 (2.492-3.126) | 9.771 (9.573-9.937) | 3.349 (3.233-3.764) |
| long spaces | 6904.156 (6884.164-6969.692) | 6.937 (5.130-8.169) | 16.286 (15.179-18.032) | 4.830 (4.321-8.466) |

Raw times are lower with disjoint observed ranges for short columns, short spaces, long columns, long spaces.
The text/raw ranges overlap for long bytes.
These are workload-specific results from this run.

Separate peak RSS samples in bytes:

| Workload | Text | Raw bytes | GNU | uutils |
|---|---:|---:|---:|---:|
| short columns | 1310720 | 1310720 | 1490944 | 1802240 |
| short spaces | 1343488 | 1310720 | 1490944 | 1802240 |
| long columns | 7651328 | 5586944 | 1556480 | 1802240 |
| long bytes | 7651328 | 5554176 | 1540096 | 1802240 |
| long spaces | 7651328 | 5586944 | 1523712 | 1802240 |

GNU, uutils and both Fern versions produce identical benchmark outputs.

The attempted 8 MiB run stopped when the parent text binary exceeded the
60-second per-process limit on the long-column workload (`-w80`) during
output validation. The table therefore uses the 1 MiB rerun, changing only
the scale parameter. No timing result is inferred for the unfinished run.
