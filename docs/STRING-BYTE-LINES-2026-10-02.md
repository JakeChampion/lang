# Byte records and binary-safe shuf

The October 3 publication refresh builds on `b3d96346b`, after checked-stdin
PR #11199 merged. It preserves the current primary-compiler test helpers and
assembly-text fixes. Bootstrap, actual stage-2 probes, Linux and Darwin
target tests, and all lint gates pass. The full suite will run in CI for
this publication.

The main integration preserves rename opcodes 356 and 357 and assigns
`memchr_bytes` opcode 358. Registry checks cover all three operations, and
the native Darwin rename test passes in 34.982 seconds. The Linux rename
tests require a native x86 host and skip in the local ARM container; CI
must cover that leg.

`ByteLineReader` reads delimited byte records from a borrowed `Reader`.
Returned arrays own their contents and remain valid after later reads or
closing the reader. Records include their delimiter when present; EOF keeps
an unterminated final record. Switching to `next_chunk_bytes()` returns the
unread buffered suffix before reading another chunk.

A read failure is sticky. If bytes were already collected, the cursor returns
that partial record once and exposes the error on the returned cursor. Later
calls return `None` without another host read. Callers inspect `error()` and
close the underlying reader themselves.

`__memchr_bytes` searches borrowed arrays without allocation. Negative starts
clamp to zero; out-of-range starts and needles return `-1`. Packed native
arrays reuse the existing vector kernels. Unpacked native arrays use slot
scans, and primary WASM scans the packed byte representation. The bootstrap WASM vector bound
uses remaining length, preventing an extreme start from overflowing into a
load outside the input.

`shuf` uses these byte records for reservoir sampling and raw spans for
whole-input mode. Its random-source buffer is also raw bytes. Echo operands
remain text, and output preserves arbitrary input bytes. File-size hints only
reserve buffer capacity; reads still continue to EOF. The WASM seek helper
now frees its scratch return area on success and failure.

## Current integration validation

In-process partial-read faults pass. The Go compiler byte-record and
byte-scan target matrix passes in 13.552 seconds. Primary byte-record,
byte-scan, `shuf` and seek checks pass in 74.548 seconds. Registry checks
pass separately and preserve the UDP operations and new `memchr_bytes`
entry. GNU `shuf` parity passes in 1.110 seconds. Darwin primary checks pass
in 38.932 seconds, the Go target matrix in 11.003 seconds, and GNU parity
in 3.368 seconds. All lint gates pass.

The initial primary WASM run caught a stale four-byte stride in the prepared
scan helper. Changing its load address to the packed byte offset fixes the
existing all-byte and boundary regression corpus. The focused WASM scan
passes in 23.080 seconds before the broader final matrix.

The published-seed bootstrap completes in 33, 27 and 24 seconds. Stages two
and three are identical: 12,464,513 bytes, SHA-256
`5805cdc13bef2d14e26f7cd2a72f691e9e4e120250d379f24b6b3a37432216e9`.
That stage-2 compiler passes 84 byte-record cases and 20 `shuf` cases across
Darwin and core WASM, with balanced allocations and frees. Byte-scan probes
also pass on those targets, Preview 2 and the primary interpreter. Compiled
scan probes verify zero allocations during repeated scans. The Preview 2
result does not claim a whole-component allocation census or Reader support.

Test durations are not performance comparisons.

The regression matrix covers every byte value, vector boundaries, extreme
scan arguments, zero allocations during repeated scans, retained aliases,
newline/NUL/high-byte delimiters, long records, cursor transitions, repeated
EOF and read failures. `shuf` cases cover file input, stdin, repeat mode,
reservoir sampling, malformed UTF-8, embedded NUL and missing delimiters.

## Native comparison

The same final compiler built both Fern implementations on arm64 macOS.
The pipeline first verified an 8,192-byte record, then changed only its size
to 8,388,608 bytes. Input contains that record of 0xff followed by a second
record containing 0x80. Every output preserves the exact records. GNU 9.12
and uutils 0.0.29 use the same input and deterministic random source.

Two warmups precede seven timed rounds with alternating command order.
Sanitizers and other local compiler jobs are absent; peak RSS is measured
separately.

| Workload | Previous Fern | Byte-based Fern | GNU | uutils |
| --- | ---: | ---: | ---: | ---: |
| Whole file, median | 12.728 ms | 12.717 ms | 11.199 ms | 11.480 ms |
| Stdin reservoir, median | 50.451 ms | 47.860 ms | 45.083 ms | 50.561 ms |
| Whole file, peak RSS | 43,876,352 B | 35,160,064 B | 9,650,176 B | 12,271,616 B |
| Reservoir, peak RSS | 81,199,104 B | 43,483,136 B | 9,715,712 B | 20,840,448 B |

Whole-file timing ranges overlap: 11.385-14.090 ms before and 11.238-13.980 ms
after. Reservoir ranges are 50.096-50.959 ms before and 47.490-48.539 ms
after. No task-owned build or test job ran during measurement, but desktop
activity was not isolated. The reservoir workload improves in this run;
the samples do not support a general throughput claim.

## Size

Both `shuf` executables are 149,409 bytes. Native code grows from 102,964 to
105,184 bytes, unwind data from 12,812 to 13,460, and the data section stays
at 7,840 bytes. Segment sizes remain unchanged.

Platform-assembled objects attribute 2,208 bytes of additional code; the
native emitter's increase is 12 bytes larger. The new byte-line routine
uses 2,456 bytes versus 2,896 for the old text routine. Raw input, byte
copying/scanning, output and ownership helpers account for the additions;
removing string joins, delimiter stripping and the old slurp loop offsets
part of that cost. For this symbol comparison, ELF `.weak` directives in
the emitted assembly were translated to Mach-O `.weak_definition` before
assembly. No executable code was changed. No size baseline changed.

The same-generator compiler comparison grows from 12,447,905 to 12,464,513
bytes. Code adds 6,096 bytes,
unwind data 272 and data 1,536 for the new intrinsic's checking, interpretation
and lowering. The text segment grows
by 16,384 bytes after crossing its alignment boundary, and the link-edit
payload grows by 224 bytes. Both compilers include the shared seek cleanup.
