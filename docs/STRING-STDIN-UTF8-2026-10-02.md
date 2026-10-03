# Whole-input bytes and checked stdin text

This slice integrates main at `db7e240f2`. The publication refresh on
2026-10-03 reproduces the compiler and reruns the byte and text probes,
size comparisons and native benchmarks. Targeted Linux tests, browser
tests and lint pass. The broader semantic matrix and full CI suite remain
pending at publication.

`io.read_all_stdin()` returns `Result[string, IoError]`. It collects raw
chunks and validates the complete input, so a UTF-8 scalar can cross a read
boundary. Malformed input returns `InvalidUtf8("stdin")`; read and close
failures return their I/O error. A failed read never becomes partial
successful text. `read_input("-")` and `read_input("")` forward that result.

`read_all_bytes(reader)` borrows a reader without closing it.
`read_all_stdin_bytes()` and `read_input_bytes(path)` preserve arbitrary
bytes. Whole-stdin helpers close their reader, preserving a read error if
close also fails. The shared collector appends each chunk with
`buf_push_bytes_range` and releases its builder on every exit path.

Compiler drivers and other text callers handle the result explicitly.
The example `tee` uses raw input and output, opens append destinations in
append mode, and reports output failures while continuing to other files.
Existing malformed file bytes survive an append.

`tsort` names are arbitrary bytes. Input, name spans, equality, hashing,
ordering and output stay in the byte domain, including cycle diagnostics.
The graph algorithm keeps GNU's seed and successor ordering. An integer
hash-table index locates collision chains; full span comparison resolves
collisions. Names retain spans of the input rather than copying each token.
No custom-map-method retention workaround is required. A regression ensures
unused hash methods contribute no target effects, including alongside an
integer-keyed map.

## Validation

The publication gate covers partial-input I/O faults for valid, malformed
and multi-chunk prefixes, the bootstrap compiler's target matrix, GNU
`env`/`tsort` parity, and primary text, raw-reader, example-`tee`, `tsort`,
unused-map-method and compiler-driver regressions. These pass, including
the primary component cases. All nine browser shim tests pass on Node 22,
as do all lint gates. The container's initial Node 18 run stopped at module
loading; CI specifies Node 22.

The combined primary run exceeded its aggregate 20-minute limit during a
component test, without an earlier assertion failure. Splitting the run
preserves coverage: the API/component group passes in 371.571 seconds;
the broader semantic matrix runs separately and remains pending. The full
unit suite passed at the earlier checkpoint; this publication's full suite
will run in CI under the project's early-publication policy.

The pinned-seed bootstrap completes in 33, 24 and 22 seconds. Stages two
and three are identical: 12,414,337 bytes, SHA-256
`a8fe015fcfa458866324ae69b450307408d3c6d57aa61edf5ebe4de8f7b2dccb`.

That exact stage-2 compiler passes 96 text cases on Darwin and core WASM:
three APIs and 16 inputs per target. Cases include empty input, NUL,
malformed and truncated encodings, and every split of two-, three- and
four-byte scalars across a read boundary. It also passes 16 `tsort`
cases covering binary names, cycles, unsigned ordering, NUL truncation,
odd token counts and distinct names sharing an FNV hash. Allocation and
free counts balance throughout.

Current main includes Preview 2 Reader framing. The reproduced primary
compiler now passes all 48 checked-text cases as components, including
malformed input and every tested scalar split. The permanent suite also
covers component raw collection and borrowed-reader lifecycle behavior.
Components check behavior; native and core-WASM runs additionally require
balanced allocation censuses.

## Size

The same final stage-2 compiler built a program that reads stdin and prints
its byte length, using current main's stdlib and the new stdlib.
Empty, NUL-containing, multi-chunk ASCII and Unicode inputs produce the
expected length on both versions.

| Artifact | Before | After |
| --- | ---: | ---: |
| Darwin executable | 50,001 bytes | 66,513 bytes |
| Darwin code section | 25,744 bytes | 28,108 bytes |
| Darwin unwind section | 4,060 bytes | 4,676 bytes |
| Darwin data section | 4,376 bytes | 4,400 bytes |
| Core WASM | 15,353 bytes | 16,692 bytes |

Native code increases by 2,364 bytes. Checked decoding and error cleanup
cross a segment alignment boundary, increasing the text segment from
32,768 to 49,152 bytes. Unwind data grows by 616 bytes and data by 24 bytes.
No size baseline changes.

Both native `tsort` executables remain 149,329 bytes. Code decreases from
109,176 to 107,752 bytes; unwind data increases from 15,228 to 15,516 bytes.
The data section remains 6,328 bytes.

## Native tsort comparison

Measured on arm64 macOS with the same final stage-2 compiler for both Fern
versions, GNU coreutils 9.12 and Rust uutils 0.12.0. The pipeline first
checks 1,000 pairs, then changes only the pair count to 100,000. Each larger
input is 2,000,000 bytes. Two warmups precede seven timed rounds with
alternating command order; every output is checked. Peak RSS is measured
separately. Sanitizers and local compiler/test jobs are absent.

The executable SHA-256 hashes are
`13ec910afe2cb0dbcd9e98e80cbc6328b0656b8c2023a0f4400f3923a90b99bd`
before and
`c975d54084f8ef8554c6e47e7a4f2135c45ccb3488b37f598870493460a9a73c`
after.

| Workload | Previous Fern | Byte-based Fern | GNU | uutils |
| --- | ---: | ---: | ---: | ---: |
| Reversed chain, median | 25.125 ms | 25.721 ms | 47.941 ms | 14.141 ms |
| Independent names, median | 28.857 ms | 26.291 ms | 45.108 ms | 14.878 ms |
| Chain, peak RSS | 30,310,400 B | 34,390,016 B | 10,944,512 B | 18,153,472 B |
| Independent names, peak RSS | 29,556,736 B | 33,587,200 B | 9,306,112 B | 14,909,440 B |

Chain timing ranges overlap: 24.673-28.636 ms before and 24.444-29.356 ms
after. Independent-name ranges also overlap: 28.107-30.559 ms before and
25.148-29.114 ms after. These workloads do not establish a general
throughput improvement. Peak memory increases with the representation that
retains input bytes, name spans and collision links.
