# Raw byte blocks and diagnostics for od

`od` carries input blocks, partial units and string-search buffers as bytes.
Diagnostics that quote a single raw option byte write bytes directly.
The change also preserves GNU's platform-specific NaN spelling: Darwin
omits a negative NaN's sign, while Linux retains it.

The reproduced builder compiler passes 62 shared GNU cases on Darwin and
core WebAssembly, comparing status, output and diagnostics. Cases cover
every byte, integer and floating units, endian selection, partial blocks,
skip and limit handling, strings, multiple files and read boundaries.
Compiler reproduction is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

Successful exits have balanced allocation censuses. Existing abrupt error
exits remain: skipped-input errors retain the same 560/584 bytes for
pipe/file on Darwin and 448/472 on core WebAssembly. Bad-option cases
retain 5,568/5,600 bytes on Darwin, 56 more than the parent, and the same
4,296/4,320 bytes on core WebAssembly. This migration does not establish
cleanup on abrupt exits.

Built by the same compiler, the combined raw-block and NaN changes add
1,232 bytes of code and 368 bytes of unwind data, and remove 256 bytes of
static data. The file shrinks from 282,257 to 282,241 bytes. Added code
handles raw ranges and byte diagnostics. No size baseline changes.

Linux raw-byte and ordinary GNU parity checks, primary target checks,
the full serial unit suite and `make lint-all` pass. Source files match
the frozen validation snapshot.

The container's GCC-built GNU oracle lacks bfloat16. A verified GNU 9.12
build with Clang 19 passes the complete corpus, including bfloat16, without
removing cases. The ordinary Darwin GNU and primary compiler parity checks
also pass, covering the platform-specific NaN spelling.

Native arm64 Darwin measurements use the reproduced builder compiler,
GNU 9.12 and uutils 0.12.0 in the C locale. The 8,192-byte pilot precedes
the same workloads at 8,388,608 bytes; long-line workloads append a newline.
Outputs are checked before two warmups and seven alternating samples.
RSS is sampled separately, with other compiler and container jobs idle.

Times are median milliseconds with observed minimum and maximum.

| Workload | Text | Raw bytes | GNU | uutils |
|---|---|---|---|---|
| hex | 84.918 (84.252-85.260) | 85.838 (84.265-89.974) | 808.919 (799.101-820.166) | 919.715 (908.829-973.946) |
| folded | 6.037 (5.676-7.687) | 5.748 (5.410-7.652) | 18.256 (17.631-19.560) | 21.975 (19.996-46.522) |
| large block | 4.945 (4.816-5.489) | 5.482 (5.223-6.298) | 17.159 (16.005-17.734) | 12.200 (11.566-15.051) |
| strings | 33.652 (33.353-34.914) | 34.081 (32.187-34.635) | 45.849 (45.317-47.309) | 3159.282 (3133.991-3253.018) |

The text/raw ranges overlap for hex, folded, large block, strings.
These are workload-specific results from this run.

Separate peak RSS samples in bytes:

| Workload | Text | Raw bytes | GNU | uutils |
|---|---:|---:|---:|---:|
| hex | 2703360 | 2637824 | 1212416 | 1916928 |
| folded | 1523712 | 1474560 | 1163264 | 1916928 |
| large block | 2490368 | 2588672 | 1359872 | 2555904 |
| strings | 1687552 | 1622016 | 1212416 | 1916928 |

GNU, uutils and both Fern versions produce identical benchmark outputs.
