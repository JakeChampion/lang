# Pr byte records

`pr` now retains input records, column cells and page bodies as byte arrays.
Private builders append raw ranges and control-byte escapes, transfer their
output and free the builder. Generated dates, headings and options remain
text. Number separators retain their byte values. Input bytes and single-byte
output no longer pass through unchecked string constructors.

The reader emits line and form-feed events. A form feed can complete a line,
pause one merge column or make a blank page. It consumes one following newline,
including across a read boundary. Cached delimiter positions avoid rescanning
a chunk with many form feeds. Records spanning reads use a growing builder.
The corpus covers backspaces at column zero, odd double-spaced pages, trailing
form feeds, independent merge-column page breaks, shared stdin and page skips.
After a failed read, descriptor type selects the directory diagnostic when
WASI returns EBADF. Ordinary reads do not perform this extra stat.

The shared date formatter also frees its builder after taking the result.
Without this cleanup, each printed header leaks a handle and its buffer.
This publication is based on `dc521da7f`; measurements from the separate
integrated string branch use a different compiler and parent.

## Validation

The frozen snapshot contains 7,908 files. All 5,594 Go, Fern and golden files
match it. Linux GNU Pr/Date/Touch/Uptime parity passes in 6.165 seconds;
primary Pr, guarded-match and printed-IR checks pass in 53.427 seconds.
Primary utility parity passes in 19.892 seconds. The full Linux unit suite
and `make lint-all` pass, without changing gate baselines.

Fresh bootstrap from `stage0-20261001-c891ebc` reaches stage2 = stage3 on
both hosts. Stage1 differs because the pin predates code-generator changes.

| Host | Fixed-point bytes | SHA-256 |
| --- | ---: | --- |
| ARM64 Linux | 12,988,096 | `7dbcd7af0bcb0ec9b7d8ae270515ac263a870fd34ce3dcd5c7a7b0438913baed` |
| ARM64 Darwin | 13,194,721 | `d23e8cf4c513e0d72263cbff6ac90149b02489510b34af7564088a75e1ef321d` |

Darwin Go Pr/Touch checks pass in 10.483 seconds, primary Pr checks in
16.623 seconds and primary utility parity in 31.006 seconds. The reproduced
compiler passes 194 native and 192 core WebAssembly Pr cases in 4.253 seconds,
with exactly one balanced allocation census per case. Two malformed-argv
cases are native-only because Wasmtime rejects them before guest execution.
Every target still exercises malformed bytes on stdin.

The expanded Darwin comparison still fails: 25 Date, 85 Pr and 33 Uptime
cases. Failure names and diagnostics exactly match the unchanged baseline,
normalizing only generated test-directory numbers. No exclusions or baseline
changes were added. These failures do not count as a passing comparison.

## Native measurements

The same reproduced compiler and stdlib build both versions. The baseline
restores both `pr.fern` and `lib/timefmt.fern` from `dc521da7f`. An 8,192-byte
pilot precedes an 8,388,608-byte workload parameter; only that parameter
changes. Repeated records fit whole into the parameter; long records append
a newline. Input is a regular file on stdin, with fixed dates and headings.
Exact stdout, stderr and status precede timing. Two warmups precede seven
alternating samples to `/dev/null`; peak RSS is measured separately. No other
task-owned compiler or container runs during timing. Desktop activity is not
isolated, and all measured outliers remain in the ranges below.

| Workload | Parent median [range] ms | Byte version median [range] ms | GNU 9.12 median ms | uutils 0.12 median ms |
| --- | --- | --- | ---: | --- |
| Short lines, headers | 43.039 [42.290, 43.953] | 31.690 [31.087, 34.435] | 47.257 | 292.364 |
| Raw bytes, three columns | 329.045 [323.877, 335.916] | 201.684 [199.960, 206.659] | 97.450 | incompatible |
| Long ASCII line | 11.379 [10.918, 13.189] | 8.996 [8.675, 9.712] | 40.315 | 15.427 |
| Long raw line | 11.903 [11.171, 12.960] | 9.353 [8.998, 9.778] | 61.000 | incompatible |

All four candidate ranges are disjoint and faster in this run. GNU matches
all outputs. uutils returns success but produces 28,521,257 instead of
9,507,085 bytes for raw columns and 25,165,825 instead of 8,388,609 bytes
for the raw line. Those runs are excluded from timing.

| Workload | Parent allocations | Byte allocations | Parent RSS bytes | Byte RSS bytes |
| --- | ---: | ---: | ---: | ---: |
| Short lines | 2,304,945 | 2,868,568 | 2,506,752 | 1,654,784 |
| Raw columns | 30,847,970 | 15,273,725 | 1,671,168 | 1,622,016 |
| Long ASCII | 788 | 518 | 70,811,648 | 43,483,136 |
| Long raw | 788 | 518 | 70,778,880 | 43,483,136 |

Every candidate census balances with zero live bytes. The parent short-line
run leaks 845,952 bytes in 17,624 blocks; its other three censuses balance.
The harness records this baseline leak and requires zero leaks from the
candidate. Short-line allocation count increases despite the leak repair.
Complete records/pages still use more memory than GNU's streaming path.

Both executables occupy 249,057 bytes. Code falls from 199,104 to 194,680
bytes, unwind data grows from 22,028 to 22,284 bytes and static data remains
14,280 bytes. Assembly instruction, literal and alignment accounting exactly
reproduces both code sizes. The 4,424-byte reduction includes smaller serial
and merge loops and removal of temporary text/clump helpers, offset by raw
reader, descriptor-stat and record helpers. Formatter cleanup adds 16 code
bytes. No size baseline changes.

Parent SHA-256: `f01fc05dcfa0db39e198c4975c8917fee149c0f9602da2486022dc8db1a155b3`.
Candidate SHA-256: `af89b1d1f563c6de1a7801f70cbccd2d5407e1f64ab01dafd580e4f1055682cb`.
