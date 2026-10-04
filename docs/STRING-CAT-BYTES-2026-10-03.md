# Raw byte input and formatting for cat

`cat` carries input chunks, control-byte spellings and expansion tables as
byte arrays. It scans and appends raw ranges directly, preserving binary
data through plain copies, visible-character formatting, numbering and
blank-line squeezing. Number prefixes and other known text remain strings.

The reproduced builder compiler passes 158 GNU cases on both Darwin and
core WASM, with exact output and balanced allocations. Coverage includes
all 64 combinations of six formatting flags, every byte value, pipes and
files, CR and blank-line state across read and file boundaries, empty files
and unterminated input. Compiler reproduction is documented in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md); this consumer
does not change compiler source.

The same compiler builds both consumer versions. Raw formatting adds
1,120 bytes of code and 328 bytes of unwind data. Static data is unchanged;
file size falls from 132,913 to 132,897 bytes. The added code implements
byte-array spellings and raw range output. No baseline changes.

Linux raw-byte and ordinary GNU parity checks, primary target checks, the
full serial unit suite and `make lint-all` pass. Source files match the
frozen validation snapshot.

Native arm64 Darwin measurements used the reproduced builder compiler,
GNU 9.12 and uutils 0.12.0 in the C locale. An 8,192-byte pilot passed
before the same workload scaled to 8,388,608 bytes. Each output was checked
before two warmups and seven alternating samples; RSS was sampled separately.
No other compiler, container or benchmark job ran during measurement.

Times below are median milliseconds with the observed minimum and maximum.
Every text/raw range overlaps, so these measurements establish no speedup.

| Workload | Text | Raw bytes | GNU | uutils |
|---|---|---|---|---|
| plain | 2.629 (2.396-3.029) | 2.489 (2.293-2.715) | 4.252 (4.011-4.454) | 2.930 (2.717-3.178) |
| visible | 7.773 (7.387-7.968) | 7.987 (7.455-8.752) | 13.750 (12.721-14.058) | 14.418 (13.509-14.859) |
| number | 5.264 (5.027-8.940) | 5.153 (4.961-5.833) | 11.874 (11.574-12.235) | 5.023 (4.930-5.268) |
| squeeze | 3.996 (3.698-4.067) | 4.015 (3.782-4.432) | 11.416 (10.134-13.449) | Output differs |
| all | 8.901 (8.195-13.489) | 8.820 (7.940-10.651) | 14.603 (13.498-18.235) | 15.310 (14.507-17.353) |
| number squeeze all | 40.487 (38.120-65.312) | 42.196 (39.953-43.219) | 15.245 (13.414-15.745) | Output differs |

Separate peak RSS samples in bytes:

| Workload | Text | Raw bytes | GNU | uutils |
|---|---:|---:|---:|---:|
| plain | 1343488 | 1343488 | 1425408 | 1785856 |
| visible | 2015232 | 2015232 | 1720320 | 1818624 |
| number | 1818624 | 1818624 | 1720320 | 1818624 |
| squeeze | 1884160 | 1884160 | 1720320 | - |
| all | 2015232 | 2015232 | 1720320 | 1818624 |
| number squeeze all | 2441216 | 2441216 | 1720320 | - |

uutils differs from GNU and both Fern versions on two full-size squeezing
workloads. Squeeze emits 8,356,219 instead of 8,356,220 bytes, first differing
at offset 2,972,417. Number/squeeze/all emits 19,530,117 instead of 19,530,126
bytes, first differing at offset 6,947,161. Those runs are excluded from
timing comparison. All pilot outputs match.
