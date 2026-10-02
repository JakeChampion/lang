# Byte records and binary-safe shuf

The refresh integrates stdin checkpoint `3730af531`, preserving
typed-IR-only lowering and the removal of unrelated map-method roots.
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
arrays reuse the existing vector kernels. Unpacked native arrays and primary
WASM arrays use their actual element stride. The bootstrap WASM vector bound
uses remaining length, preventing an extreme start from overflowing into a
load outside the input.

`shuf` uses these byte records for reservoir sampling and raw spans for
whole-input mode. Its random-source buffer is also raw bytes. Echo operands
remain text, and output preserves arbitrary input bytes. File-size hints only
reserve buffer capacity; reads still continue to EOF. The WASM seek helper
now frees its scratch return area on success and failure.

## Current integration validation

The source is based on stdin commit `3730af531`. In-process partial-read
faults and the Go compiler's byte-record and byte-scan target matrix pass
in 14.500 seconds. Primary byte-record, byte-scan, `shuf` and seek tests
pass. After reconciling the registry totals for both `tcp_send_bytes` and
`memchr_bytes`, the opcode and lift-admission checks pass in 1.257 seconds.
GNU `shuf` parity passes in 0.940 seconds. The full unit suite and all lint
gates pass on the final source.

The published-seed bootstrap completes in 25, 21 and 19 seconds. Stages two
and three are identical: 12,411,905 bytes, SHA-256
`857784a4d4a5ec1d690ddb914f0b294b88650f98b437ef4f6474bbcc5bada346`.
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
| Whole file, median | 15.167 ms | 14.683 ms | 11.933 ms | 12.393 ms |
| Stdin reservoir, median | 54.260 ms | 50.028 ms | 47.075 ms | 52.924 ms |
| Whole file, peak RSS | 43,827,200 B | 35,176,448 B | 9,584,640 B | 12,271,616 B |
| Reservoir, peak RSS | 81,182,720 B | 43,499,520 B | 9,715,712 B | 20,824,064 B |

Both timing ranges overlap: whole-file timings are 13.838-29.098 ms before
and 12.918-16.122 ms after; reservoir timings are 51.831-57.741 ms before
and 49.187-54.616 ms after. This run establishes lower peak memory use for
these workloads, not a throughput improvement.

## Size

Both `shuf` executables are 149,409 bytes. Native code grows from 101,748 to
103,772 bytes, unwind data from 12,812 to 13,460, and the data section stays
at 7,840 bytes. Segment sizes remain unchanged.

Platform-assembled objects attribute 2,004 bytes of additional code; the
native emitter's increase is 20 bytes larger. The new byte-line routine
uses 2,432 bytes versus 2,876 for the old text routine. Raw input, byte
copying/scanning, output and ownership helpers account for the additions;
removing string joins, delimiter stripping and the old slurp loop offsets
part of that cost. For this symbol comparison, ELF `.weak` directives in
the emitted assembly were translated to Mach-O `.weak_definition` before
assembly. No executable code was changed. No size baseline changed.

The compiler grows from 12,411,809 to 12,411,905 bytes. Code adds 5,776 bytes,
unwind data 272 and data 1,280 for the new intrinsic's checking, interpretation
and lowering. Text and data file segments stay the same; the link-edit
payload grows by 96 bytes. Both compilers include the shared seek cleanup.
