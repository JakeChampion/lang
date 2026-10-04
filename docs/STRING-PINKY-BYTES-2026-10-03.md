# Raw file output for pinky

Long-format `pinky` writes headers and `.project`/`.plan` contents through
one buffered writer. File chunks stay in byte arrays. The previous version
appended every chunk to a string and accumulated all requested users' output
before writing it.

Opening a missing file still suppresses its label. A file that opens but
fails to read still prints the label, and every opened reader is closed.
The private fixture calls the implementation's actual file-output function.
It covers missing files, directories, empty files and all-byte inputs around
4 KiB and 64 KiB read boundaries. All twelve cases pass on Darwin and core
WebAssembly with balanced allocations. Linux primary x86-64, ARM64 and
WASM tests pass, as do GNU parity, primary parity, the full unit suite and
all lint gates. Darwin's raw-file tests and primary parity also pass.
Tests never alter real home files, passwd or utmp.

The supplemental Darwin GNU corpus has 19 existing differences: eighteen
live-login cases and the multi-user case containing `nobody`. The unchanged
parent produces the same failed case names and identical Fern output.
The login reader uses Linux's `/var/run/utmp` layout, while Darwin keeps
`utmpx`; the passwd parser rejects the signed ID in Darwin's `nobody` entry.
These platform limitations remain. No test exclusions or baseline changes
were added for them.

The same reproduced compiler described in [the CRC report](STRING-CKSUM-BYTES-2026-10-03.md)
builds both full `pinky` executables. Code grows from 143,324 to 143,440 bytes
and unwind data from 18,436 to 18,612 bytes for raw input and streaming output.
Static data remains 24,248 bytes and the executable remains 199,409 bytes.
No size baseline changes.

The parent executable SHA-256 is
`b0828816e9c1217f4b139c7c312d6c007617822074918e332cbaa0671f612ef0`;
the candidate is
`0313f195812497ca08ea7aba83c6e7cd6c92f527bbb29588fb95994afaa87058`.

The native arm64 Darwin benchmark extracts the actual file-output function
from each version and supplies two private files. It measures this helper,
not passwd lookup or an end-to-end user query. GNU 9.12 `cat` and uutils
0.12.0 `cat` copy the same files and labels as reference copy workloads.
They open separate label files, while Fern supplies those labels as literals.

An 8,192-byte-per-file pilot precedes 8,388,608 bytes per file, changing only
that size. Every input cycles through all 256 byte values. All four commands
produce identical output and empty stderr, and exit successfully at both
scales. Two warmups precede seven alternating samples with this task's
compiler and container jobs idle. Output goes to `/dev/null` during timing; exact bytes
are checked separately. Peak RSS is also measured separately.

| Implementation | Median ms | Range ms | Peak RSS bytes |
| --- | ---: | ---: | ---: |
| Previous file-output helper | 16.431 | 15.293-18.698 | 89,391,104 |
| Raw streaming helper | 6.538 | 5.881-7.546 | 1,097,728 |
| GNU cat | 5.262 | 4.957-5.799 | 1,425,408 |
| uutils cat | 4.386 | 3.679-5.288 | 1,785,856 |

The before/after timing ranges do not overlap in this run. Both reference
copy commands are faster; the migration removes retained file contents and
preserves bytes without changing the platform I/O implementation.
