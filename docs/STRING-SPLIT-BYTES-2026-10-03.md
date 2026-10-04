# Raw input for split

`split` reads, spools, scans and writes byte arrays. File contents no longer
pass through strings. Output ranges borrow their input block, and line mode
counts each block once instead of rescanning the remaining suffix.

Round-robin mode now writes each block as it arrives. Previously, its loop
kept reading until EOF before distributing any records. A trailing fragment
stays assigned to the same output until its separator arrives, so long records
need no accumulated copy. For `-C`, an incomplete record grows in a builder;
each additional read is scanned once. This removes repeated copies and scans
of the entire growing record.

Allocation checks also exposed an existing unused placeholder output buffer.
It retained two allocations on every successful run, including empty input.
The unopened output now shares the stdout placeholder until a real piece is
opened, and successful runs balance allocations.

The shared comparison covers 196 GNU file and pipe cases, including every
byte value, NUL separators, read boundaries, long records and partially filled
output files. A separate test leaves stdin open and requires round-robin output
to appear before EOF. The reproduced compiler passes these checks on Darwin
and core WebAssembly. The WASM runner explicitly grants the private fixture
directory and passes its temporary-directory setting to the guest.
Compiler reproduction is recorded in [the CRC report](STRING-CKSUM-BYTES-2026-10-03.md).
The test fails on the unchanged parent because output remains empty while
stdin is open. Linux primary x86-64, ARM64 and core WebAssembly checks pass,
as do Darwin primary checks, both platforms' GNU corpora and primary parity,
the full Linux unit suite and all lint gates.

Both native versions use that same compiler. Code grows from 131,352 to
131,960 bytes, unwind data from 14,148 to 14,788 bytes, and static data stays
at 10,656 bytes. The raw conversion and placeholder fix initially reduced
code by 688 bytes. The long-record builder adds 1,296 bytes, giving the final
608-byte increase. Its measured speed and memory improvements justify that
cost. The executable grows from 166,097 to 182,609 bytes: a 16,384-byte segment
alignment step plus 128 bytes of signing data. No size baseline changes.

The parent executable SHA-256 is
`247a09ce1ed39d33140763770acc671581aebf74189a02a5ff5091e757c1b43d`;
the candidate is
`ebc843cc49fba779b47b4a36f30f036f8b8dfd0e3ae3fd487f8390e5de3509d4`.

Native arm64 Darwin measurements compare GNU 9.12 and uutils 0.12.0. An
8,192-byte pilot precedes an 8,388,608-byte run, changing only the input size.
Inputs cycle through all 256 bytes, or contain one unterminated record of
0xff bytes. All four implementations produce identical file names, file
contents, stdout, stderr and exit status at both scales. Two warmups precede
seven alternating samples, with compiler and container jobs idle. Peak RSS
is measured separately. File deletion and output verification are outside
the timed interval; process startup and file creation are included.

The long-record and round-robin timing ranges do not overlap with their
parent ranges. Byte, line and ordinary `-C` timings overlap. GNU and uutils
remain faster than Fern on the long-record `-C` workload. Round-robin peak
RSS drops from roughly 65 MB to roughly 2 MB for these inputs.

| Workload | Implementation | Median ms | Range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| bytes | text | 6.899 | 4.706-12.534 | 1,458,176 |
| bytes | bytes | 6.070 | 4.518-11.342 | 1,376,256 |
| bytes | gnu | 8.827 | 6.061-11.918 | 1,441,792 |
| bytes | uutils | 11.611 | 8.461-12.504 | 1,998,848 |
| lines | text | 15.159 | 14.734-17.987 | 2,064,384 |
| lines | bytes | 15.468 | 14.980-17.538 | 1,654,784 |
| lines | gnu | 16.150 | 15.563-18.497 | 1,458,176 |
| lines | uutils | 67.190 | 60.312-87.571 | 1,966,080 |
| line_bytes | text | 5.761 | 5.418-6.880 | 1,851,392 |
| line_bytes | bytes | 5.890 | 5.299-6.351 | 1,851,392 |
| line_bytes | gnu | 7.275 | 6.569-7.676 | 1,441,792 |
| line_bytes | uutils | 48.868 | 48.505-52.743 | 1,998,848 |
| long_record | text | 28.160 | 27.848-31.574 | 62,160,896 |
| long_record | bytes | 8.112 | 7.347-13.055 | 26,673,152 |
| long_record | gnu | 7.284 | 6.984-7.641 | 1,441,792 |
| long_record | uutils | 7.004 | 6.406-7.167 | 10,420,224 |
| round_robin | text | 75.489 | 74.922-76.456 | 65,994,752 |
| round_robin | bytes | 7.575 | 6.998-7.697 | 1,736,704 |
| round_robin | gnu | 15.702 | 14.894-22.267 | 1,458,176 |
| round_robin | uutils | 50.716 | 49.172-55.736 | 1,966,080 |
| round_long_record | text | 11.870 | 11.347-12.536 | 65,028,096 |
| round_long_record | bytes | 6.211 | 5.306-6.485 | 2,097,152 |
| round_long_record | gnu | 6.999 | 5.671-8.374 | 1,490,944 |
| round_long_record | uutils | 6.984 | 6.348-8.949 | 10,420,224 |
| round_one | text | 3.950 | 3.570-4.181 | 1,523,712 |
| round_one | bytes | 3.200 | 3.105-3.547 | 1,523,712 |
| round_one | gnu | 5.028 | 4.579-5.469 | 1,507,328 |
| round_one | uutils | 7.032 | 6.625-7.277 | 1,982,464 |
