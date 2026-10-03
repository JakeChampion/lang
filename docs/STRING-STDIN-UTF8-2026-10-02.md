# Whole-input bytes and checked stdin text

This slice integrates completed byte-view and diagnostic checkpoint
`730a790e0`. Target tests, browser tests, bootstrap, actual stage-2 probes,
the full unit suite and all lint gates pass.

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

The integrated Linux run passes partial-input I/O faults for valid,
malformed and multi-chunk prefixes, the bootstrap compiler's target matrix,
GNU `env`/`tsort` parity, and primary text, raw-reader, example-`tee`,
`tsort`, unused-map-method and compiler-driver regressions. The partial-read
pilot takes 0.070 seconds, the bootstrap target matrix 36.033 seconds,
GNU parity 1.167 seconds and the primary matrix 107.775 seconds. The full
unit suite and all lint gates pass. All nine browser shim tests pass with
no skips. Test durations are not performance comparisons.

The pinned-seed bootstrap completes in 33, 26 and 23 seconds. Stages two
and three are identical: 12,480,129 bytes, SHA-256
`f8b7ea0eded0b223510efb0912396d4b2ae8b0f3ac675e3f542660b84745ce2b`.
They also match the completed D8 compiler byte for byte.

That exact stage-2 compiler passes 96 text cases on Darwin and core WASM:
three APIs and 16 inputs per target. Cases include empty input, NUL,
malformed and truncated encodings, and every split of two-, three- and
four-byte scalars across a read boundary. It also passes 16 `tsort`
cases covering binary names, cycles, unsigned ordering, NUL truncation,
odd token counts and distinct names sharing an FNV hash. Allocation and
free counts balance throughout.

Bootstrap Preview 2 tests pass. Primary Preview 2 stdin remains unsupported:
the unchanged compiler refuses both the old `read_chunk` and the new
`read_chunk_bytes` because its component framing has no Reader imports.
Primary core-WASM results do not establish Preview 2 support. Adding that
framing remains separate target work.

## Size

The same final stage-2 compiler built a program that reads stdin and prints
its byte length, using the D8 checkpoint's stdlib and the new stdlib.
Empty, NUL-containing, multi-chunk ASCII and Unicode inputs produce the
expected length on both versions.

| Artifact | Before | After |
| --- | ---: | ---: |
| Darwin executable | 50,001 bytes | 66,513 bytes |
| Darwin code section | 26,264 bytes | 28,636 bytes |
| Darwin unwind section | 4,060 bytes | 4,676 bytes |
| Darwin data section | 4,376 bytes | 4,400 bytes |
| Core WASM | 15,423 bytes | 16,734 bytes |

Native code increases by 2,372 bytes. Checked decoding and error cleanup
cross a segment alignment boundary, increasing the text segment from
32,768 to 49,152 bytes. Unwind data grows by 616 bytes and data by 24 bytes.
No size baseline changes.

Both native `tsort` executables remain 149,329 bytes. Code decreases from
111,968 to 110,392 bytes; unwind data increases from 15,180 to 15,468 bytes.
The data section remains 6,328 bytes.

## Native tsort comparison

Measured on arm64 macOS with the same final stage-2 compiler for both Fern
versions, GNU coreutils 9.12 and Rust uutils 0.0.29. The pipeline first
checks 1,000 pairs, then changes only the pair count to 100,000. Each larger
input is 2,000,000 bytes. Two warmups precede seven timed rounds with
alternating command order; every output is checked. Peak RSS is measured
separately. Sanitizers and local compiler/test jobs are absent. Ordinary OS
background services, including indexing, were active around the run.

The executable SHA-256 hashes are
`4249a24a838a94d205fc8f995f3fafbb569d63cd471b2b8be9f90efd7e840b6c`
before and
`45ce6841d7808d6d8f14aeb119a803fa7e0a1936a028d7aee3886bf3e880539a`
after.

| Workload | Previous Fern | Byte-based Fern | GNU | uutils |
| --- | ---: | ---: | ---: | ---: |
| Reversed chain, median | 25.427 ms | 25.489 ms | 47.287 ms | 146.808 ms |
| Independent names, median | 29.291 ms | 26.683 ms | 43.168 ms | 144.995 ms |
| Chain, peak RSS | 30,310,400 B | 34,390,016 B | 10,944,512 B | 28,459,008 B |
| Independent names, peak RSS | 29,556,736 B | 33,587,200 B | 9,306,112 B | 22,233,088 B |

Chain timing ranges overlap: 24.616-25.910 ms before and 24.751-26.616 ms
after. Independent-name samples are separated in this run: 28.469-29.941 ms
before and 25.935-27.597 ms after. These workloads do not establish a general
throughput improvement. Peak memory increases with the representation that
retains input bytes, name spans and collision links.
