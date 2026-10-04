# Raw input for cksum

The CRC, CRC32b, BSD and System V modes read byte arrays. CRC uses
`__crc32_cksum_array`, which borrows its input and shares the existing
accelerated native kernel. The other modes use their byte-array entry
points. Digest modes and checksum-file parsing remain separate work.

An independent bit-at-a-time reference checks five initial CRC values at
210 input lengths, including vector and 65,536-byte read boundaries.
Streaming splits, shared input and allocation stability are covered too.
The reproduced compiler passes these checks on Darwin and core WebAssembly
with balanced allocations; the component behavior checks also pass.
Each native/core target additionally passes 96 GNU comparisons across
four algorithms, 12 input lengths and normal/raw output.

The compiler reproduces from pinned stage0 `stage0-20261001-c891ebc`.
Stage 1 takes 34 seconds and produces 12,249,857 bytes. Stages 2 and 3
take 25 and 24 seconds and produce identical 12,563,585-byte executables,
SHA-256 `fe74d3c8526b1bd0871b505f5fa39862428ba1d70c0c9a55dfbd738c0b53e4b8`.
The later Crc32 array-wrapper change passes the same reproduced-compiler
consumer checks. Linux bootstrap and primary targets, per-module ARM64
linking, GNU comparisons, the full unit suite and all lint gates pass.
The Darwin GNU corpus, raw-byte checks and primary-compiler parity also pass.

Using that compiler for both consumer versions, native code grows from
321,596 to 323,684 bytes. Assembly symbol attribution accounts for all
2,088 added bytes: raw-reader functions add 844, checksum entry points
620, result cleanup 612 and changes in existing functions 12. An initial
call to the overloaded view API retained unrelated methods; the matching
Crc32 array API removes 2,504 bytes of that avoidable code.

Unwind data grows from 21,332 to 21,844 bytes. Static data stays at 14,240.
The text segment crosses a 16,384-byte alignment boundary, and the code
signature grows by 128 bytes, taking the file from 364,609 to 381,121 bytes.
The remaining cost supplies raw ingestion while digest modes still retain
the text reader. No size baseline changes.

The baseline executable SHA-256 is
`d3e8fa75a3c09d6fae6ed69881d0419198e6732b5718a8b7d2c58e4d7ffe708f`;
the candidate is
`d5b3dd74f1993539e07828a103d218f623a364ef5ffb15a48f40d59d090982bf`.

Native arm64 Darwin benchmarks compare GNU 9.12 and uutils 0.12.0.
An 8,192-byte pilot precedes an 8,388,608-byte run, changing only the
input-size parameter. Inputs contain repeating bytes 0 through 255 or
zeros. All four implementations agree on output, stderr and status at
both scales. Two warmups precede seven alternating samples, with other
compiler and container jobs idle. Peak RSS is measured separately.
All before/after timing ranges overlap; these measurements establish
no clear throughput change.

| Workload | Implementation | Median ms | Range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| crc_pattern | text | 2.714 | 2.567-3.575 | 1,409,024 |
| crc_pattern | bytes | 2.700 | 2.541-3.373 | 1,441,792 |
| crc_pattern | gnu | 7.472 | 7.312-8.175 | 1,245,184 |
| crc_pattern | uutils | 3.086 | 2.871-3.914 | 1,982,464 |
| crc_zeros | text | 2.580 | 2.513-2.683 | 1,409,024 |
| crc_zeros | bytes | 2.611 | 2.501-2.617 | 1,409,024 |
| crc_zeros | gnu | 6.073 | 5.847-6.354 | 1,294,336 |
| crc_zeros | uutils | 2.765 | 2.636-3.393 | 1,982,464 |
| crc32b_pattern | text | 9.800 | 9.013-10.083 | 1,441,792 |
| crc32b_pattern | bytes | 9.687 | 9.265-10.331 | 1,409,024 |
| crc32b_pattern | gnu | 7.718 | 7.479-8.203 | 1,245,184 |
| crc32b_pattern | uutils | 3.074 | 2.819-3.431 | 1,966,080 |
| crc32b_zeros | text | 9.092 | 8.731-9.504 | 1,441,792 |
| crc32b_zeros | bytes | 9.126 | 8.762-9.818 | 1,409,024 |
| crc32b_zeros | gnu | 7.457 | 7.294-7.828 | 1,245,184 |
| crc32b_zeros | uutils | 2.766 | 2.673-2.879 | 1,998,848 |
| bsd_pattern | text | 12.883 | 12.682-12.954 | 1,392,640 |
| bsd_pattern | bytes | 12.942 | 12.794-13.171 | 1,359,872 |
| bsd_pattern | gnu | 14.646 | 14.436-15.425 | 1,294,336 |
| bsd_pattern | uutils | 11.205 | 11.068-11.509 | 1,949,696 |
| bsd_zeros | text | 13.058 | 12.905-14.512 | 1,359,872 |
| bsd_zeros | bytes | 13.316 | 12.940-14.011 | 1,359,872 |
| bsd_zeros | gnu | 14.973 | 14.269-16.023 | 1,245,184 |
| bsd_zeros | uutils | 11.427 | 11.263-12.512 | 1,949,696 |
| sysv_pattern | text | 2.891 | 2.697-3.984 | 1,359,872 |
| sysv_pattern | bytes | 2.856 | 2.768-3.058 | 1,359,872 |
| sysv_pattern | gnu | 4.417 | 4.255-4.513 | 1,245,184 |
| sysv_pattern | uutils | 2.952 | 2.660-3.153 | 1,949,696 |
| sysv_zeros | text | 2.896 | 2.726-3.000 | 1,359,872 |
| sysv_zeros | bytes | 2.875 | 2.704-3.799 | 1,359,872 |
| sysv_zeros | gnu | 4.510 | 4.288-4.667 | 1,294,336 |
| sysv_zeros | uutils | 2.864 | 2.768-3.167 | 1,949,696 |
