# Byte records and binary-safe shuf

The October 3 refresh integrates stdin checkpoint `3b3c349de`, including
allocation-free string byte views and typed escape diagnostics.
Bootstrap, actual stage-2 probes, target tests, the full unit suite and
all lint gates pass.

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

The source is based on stdin commit `3b3c349de`. In-process partial-read
faults pass. The final Go compiler byte-record and byte-scan target matrix
passes in 11.706 seconds. Primary byte-record, byte-scan, `shuf`, seek and
registry tests pass in 75.155 seconds. Registry checks preserve the incoming
UDP operations and the new `memchr_bytes` entry. GNU `shuf` parity passes in
1.007 seconds. The full unit suite and all lint gates pass on the final source.

The initial primary WASM run caught a stale four-byte stride in the prepared
scan helper. Changing its load address to the packed byte offset fixes the
existing all-byte and boundary regression corpus. The focused WASM scan
passes in 23.080 seconds before the broader final matrix.

The published-seed bootstrap completes in 36, 26 and 23 seconds. Stages two
and three are identical: 12,496,737 bytes, SHA-256
`b95c45f5d33144f6d17c650f684e8ef0b0f17f73d3c814f52c36936475b8e383`.
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
| Whole file, median | 23.406 ms | 13.540 ms | 12.040 ms | 11.310 ms |
| Stdin reservoir, median | 50.780 ms | 48.252 ms | 45.707 ms | 49.401 ms |
| Whole file, peak RSS | 43,827,200 B | 35,176,448 B | 9,584,640 B | 12,271,616 B |
| Reservoir, peak RSS | 81,166,336 B | 43,499,520 B | 9,764,864 B | 20,824,064 B |

Whole-file timings are noisy: 11.697-83.696 ms before and 10.995-106.321 ms
after, with large outliers in GNU and uutils too. Reservoir ranges are
50.102-51.895 ms before and 47.567-50.070 ms after. No task-owned build or
test job ran during measurement, but desktop activity was not isolated.
The peak-memory reduction is clear in these workloads; the timing samples
do not support a general throughput claim.

## Size

Both `shuf` executables are 149,409 bytes. Native code grows from 103,124 to
105,340 bytes, unwind data from 12,812 to 13,460, and the data section stays
at 7,840 bytes. Segment sizes remain unchanged.

Platform-assembled objects attribute 2,204 bytes of additional code; the
native emitter's increase is 12 bytes larger. The new byte-line routine
uses 2,476 bytes versus 2,916 for the old text routine. Raw input, byte
copying/scanning, output and ownership helpers account for the additions;
removing string joins, delimiter stripping and the old slurp loop offsets
part of that cost. For this symbol comparison, ELF `.weak` directives in
the emitted assembly were translated to Mach-O `.weak_definition` before
assembly. No executable code was changed. No size baseline changed.

The same-generator compiler comparison grows from 12,480,129 to 12,496,737
bytes. Code adds 5,752 bytes,
unwind data 272 and data 1,280 for the new intrinsic's checking, interpretation
and lowering. The text segment stays the same; the data file segment grows
by 16,384 bytes after crossing its alignment boundary, and the link-edit
payload grows by 224 bytes. Both compilers include the shared seek cleanup.
