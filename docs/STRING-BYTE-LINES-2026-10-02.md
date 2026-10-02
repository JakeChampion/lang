# Byte records and binary-safe shuf

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

The source is based on stdin commit `1573fe231`. In-process partial-read
faults, byte-scan type contracts, borrowing and WASM helper-registration
checks pass. The bootstrap target matrix passes in 13.237 seconds and GNU
`shuf` parity in 0.974 seconds. Primary x86-64, ARM64 and core-WASM tests,
including ownership and IR registration checks, pass in 79.598 seconds.
Darwin primary and interpreter tests pass in 30.707 seconds. All lint gates
pass after expressing the interpreter's array guard with `if let`; the
wildcard-match ceiling is unchanged.

The published-seed bootstrap completes in 25, 23 and 24 seconds. Stages two
and three are identical: 12,130,833 bytes, SHA-256
`7bf1d72975ceda529b77b53eb1deb8682865d745cad03dac120d8ee55691e29c`.
That stage-2 compiler passes 84 byte-record cases and 20 `shuf` cases across
Darwin and core WASM, with balanced allocations and frees. Byte-scan probes
also pass on those targets, Preview 2 and the primary interpreter. Compiled
scan probes verify zero allocations during repeated scans. The Preview 2
result does not claim a whole-component allocation census or Reader support.

Full-unit validation remains pending. Test durations are not performance
comparisons.

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
| Whole file, median | 12.027 ms | 12.272 ms | 10.004 ms | 10.466 ms |
| Stdin reservoir, median | 51.015 ms | 48.973 ms | 46.021 ms | 51.379 ms |
| Whole file, peak RSS | 43,810,816 B | 35,160,064 B | 9,584,640 B | 12,271,616 B |
| Reservoir, peak RSS | 81,166,336 B | 43,401,216 B | 9,682,944 B | 20,791,296 B |

Whole-file timing ranges overlap: 11.760-12.400 ms before and
11.825-12.462 ms after. Reservoir ranges are 50.635-52.156 ms before and
47.825-49.828 ms after. The reservoir improvement applies to this measured
long-record workload; it is not a claim about all inputs.

## Size

Both `shuf` executables are 149,409 bytes. Native code grows from 101,684 to
103,272 bytes, unwind data from 12,812 to 13,396, and the data section stays
at 7,840 bytes. Segment sizes remain unchanged.

Platform-assembled objects attribute 1,568 bytes of additional code; the
native emitter's increase is 20 bytes larger. The new byte-line routine
uses 2,432 bytes versus 2,876 for the old text routine. Raw input, byte
copying/scanning, output and ownership helpers account for the additions;
removing string joins, delimiter stripping and the old slurp loop offsets
part of that cost. No size baseline changed.

The compiler grows from 12,114,209 to 12,130,833 bytes. Code adds 5,920 bytes,
unwind data 280 and data 1,536 for the new intrinsic's checking, interpretation
and lowering plus seek cleanup. The text segment crosses a 16 KiB boundary;
link-edit payload adds 240 bytes, including 128 bytes of code signature.
