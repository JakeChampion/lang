# Raw records and retained keys for uniq

`uniq` reads byte arrays and compares ranges of those arrays. The held
comparison key is a range of the already retained record buffer. Lines
that span reads accumulate in a byte builder, avoiding repeated copies
of a growing prefix. Delimiter scanning, field scanning and equality use
the existing byte kernels; C-locale case folding and group behavior stay
the same.

The shared GNU comparison covers 55 cases through both pipe and file
input: every byte, newline and NUL terminators, output files, group and
count modes, field and character skips, widths, case folding, unterminated
records and read boundaries. The reproduced compiler passes all 110
executions on Darwin and core WebAssembly, with balanced allocations.
Compiler reproduction is recorded in
[the builder report](STRING-BYTE-BUILDER-MAPS-2026-10-03.md).

The same compiler produces 96,480 bytes of code, up from 96,180, and
11,772 bytes of unwind data, up from 11,548. Static data stays at 9,144
bytes and the executable remains 132,977 bytes. The additional builder
and raw-reader paths account for the growth. No size baseline changes.

Linux raw-byte checks, primary target checks, the GNU corpus, primary
parity, the full unit suite and all lint checks pass. The Darwin raw-byte
checks, GNU corpus and primary parity also pass. The existing
`TestUniqParity` Darwin exception is removed.

Native arm64 Darwin measurements use GNU 9.12 and uutils 0.12.0. An
8,192-byte scale pilot precedes an 8,388,608-byte scale run, changing only
that parameter. Short workloads contain 8,388,609 bytes; long plain input
contains 16,777,218 and long field input 16,777,231. Both Fern versions and
uutils match GNU output, status and stderr for every workload at both
scales. Two warmups precede seven alternating samples. Peak RSS is
measured separately, with other compiler and container jobs idle.

Field and case-fold comparisons improve with disjoint timing ranges.
Long plain and field records also improve and use less sampled memory.
The short plain and count timing ranges overlap.

| Workload | Implementation | Median ms | Range ms | Peak RSS bytes |
| --- | --- | ---: | ---: | ---: |
| short_plain | text | 12.639 | 11.652-13.355 | 1,589,248 |
| short_plain | bytes | 11.969 | 11.596-12.988 | 1,540,096 |
| short_plain | gnu | 25.632 | 24.789-26.527 | 1,196,032 |
| short_plain | uutils | 17.429 | 16.946-18.229 | 2,080,768 |
| short_count | text | 16.783 | 16.454-17.559 | 1,540,096 |
| short_count | bytes | 16.420 | 15.958-17.331 | 1,540,096 |
| short_count | gnu | 33.787 | 32.876-34.266 | 1,196,032 |
| short_count | uutils | 21.315 | 20.853-21.651 | 2,080,768 |
| short_fields | text | 23.613 | 22.840-24.726 | 1,556,480 |
| short_fields | bytes | 20.104 | 19.453-20.492 | 1,556,480 |
| short_fields | gnu | 42.579 | 41.645-43.643 | 1,196,032 |
| short_fields | uutils | 20.483 | 20.301-21.154 | 2,080,768 |
| short_fold | text | 24.812 | 23.928-25.635 | 1,556,480 |
| short_fold | bytes | 21.536 | 21.192-22.291 | 1,540,096 |
| short_fold | gnu | 37.382 | 36.903-37.512 | 1,196,032 |
| short_fold | uutils | 17.988 | 17.711-18.390 | 2,080,768 |
| long_plain | text | 14.434 | 13.942-14.831 | 78,954,496 |
| long_plain | bytes | 10.288 | 9.912-10.679 | 43,810,816 |
| long_plain | gnu | 23.255 | 22.599-23.570 | 32,669,696 |
| long_plain | uutils | 9.693 | 9.215-10.910 | 35,553,280 |
| long_fields | text | 15.762 | 15.410-16.781 | 87,359,488 |
| long_fields | bytes | 10.869 | 10.583-11.482 | 43,810,816 |
| long_fields | gnu | 23.654 | 22.784-24.113 | 32,587,776 |
| long_fields | uutils | 9.475 | 9.375-9.820 | 43,810,816 |
