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

Bootstrap reproducibility, actual stage-2 probes, and full-unit validation
remain pending. Historical results from the prepared branches are not
evidence for this integration. Test durations are not performance comparisons.

The regression matrix covers every byte value, vector boundaries, extreme
scan arguments, zero allocations during repeated scans, retained aliases,
newline/NUL/high-byte delimiters, long records, cursor transitions, repeated
EOF and read failures. `shuf` cases cover file input, stdin, repeat mode,
reservoir sampling, malformed UTF-8, embedded NUL and missing delimiters.

Native timing and size measurements remain pending. The comparison uses the
same final compiler for the previous and new implementations, verifies an
8,192-byte pilot, then changes only the record size to 8,388,608 bytes. No
performance or size baseline has been changed.
