# Whole-input bytes and checked stdin text

This slice integrates producer checkpoint `c75e0f6f2` and preserves
typed-IR-only lowering. Target tests, browser tests, bootstrap and actual
stage-2 probes, the full unit suite and all lint gates pass.

The refresh removes an earlier map-method retention workaround. It could
keep an unrelated Hash implementation whenever any map was reachable,
wrongly adding target effects to valid programs. The integer-index `tsort`
does not need that workaround. A regression checks unused hash methods both
without a map and alongside an integer-keyed map. General custom-map-key
retention is separate from this slice.

`io.read_all_stdin()` now returns `Result[string, IoError]`. It collects
raw chunks and validates the complete input, so a UTF-8 scalar can cross a
read boundary. Malformed input returns `InvalidUtf8("stdin")`; read and
close failures return their I/O error. A failed read never becomes partial
successful text. `read_input("-")` and `read_input("")` forward that result.

`read_all_bytes(reader)` borrows a reader without closing it. The
`read_all_stdin_bytes()` and `read_input_bytes(path)` helpers preserve
arbitrary bytes. Whole-stdin helpers close their reader, preserving a read
error if close also fails. The shared collector appends each chunk with
`buf_push_bytes_range` and releases its builder on every exit path.

Compiler drivers and other text callers handle the result explicitly.
The example `tee` uses raw input and output, opens append destinations in
append mode, and reports output failures while continuing to other files.
Existing malformed file bytes survive an append.

`tsort` names are arbitrary bytes. Its input, name spans, equality, hashing,
ordering and output now stay in the byte domain, including cycle diagnostics.
The graph algorithm keeps GNU's seed and successor ordering. An integer
hash-table index locates collision chains; full span comparison resolves
collisions. Names retain spans of the input rather than copying each token.

## Validation

On the integration with producer checkpoint `c75e0f6f2`:

- Partial-input I/O fault tests pass for valid, malformed and multi-chunk
  prefixes. The Go compiler target matrix passes in 82.936 seconds.
- Primary text, raw-reader, example-tee, `tsort`, unused-map-method and
  compiler-driver regressions pass in 178.221 seconds.
- The actual final stage-2 compiler passes 96 additional text cases:
  three APIs, 16 inputs each, on Darwin and core WASM. All allocation and
  free counts balance. Cases include NUL, empty input, malformed sequences,
  truncated final input, and every split of two-, three- and four-byte scalars.

All nine browser shim tests pass. The expanded GNU `env`/`tsort` parity
suite passes in 2.523
seconds, including binary names from files and stdin, cycles, NUL truncation,
unsigned byte ordering and distinct names with the same FNV hash. Primary
`tsort` regressions pass on native and core-WASM targets with balanced
ownership. An unused hash implementation contributes no target effects,
including when an unrelated integer-keyed map is reachable.

The refreshed published-seed bootstrap completes in 26, 22 and 19 seconds.
Stages two and three are identical: 12,081,745 bytes, SHA-256
`a4e86e7e079ab08a0f4957ad7ff1eef7e1b17f12b66c88d6e044531e05d111d1`.
They also match the producer checkpoint's final compiler byte for byte.
The final stage-2 compiler repeats all 96 text cases and 16 `tsort` cases on
Darwin/core WASM with balanced ownership. The final integer-index `tsort`
also passes the GNU parity and primary target suites. The refreshed full
unit suite and all lint gates pass. Validation durations are not
performance comparisons.

Bootstrap Preview 2 tests pass. Primary Preview 2 stdin remains unsupported:
the unchanged compiler refuses both the old `read_chunk` and the new
`read_chunk_bytes` because its component framing has no Reader imports.
Primary core-WASM results do not establish Preview 2 support. Adding that
framing remains separate target work.

## Size

The same stage-2 compiler built a program that reads stdin and prints its
byte length, using the producer checkpoint and new stdlib respectively.
Empty, NUL-containing, multi-chunk ASCII and Unicode inputs produced the
expected length on both versions.

| Artifact | Before | After |
| --- | ---: | ---: |
| Darwin executable | 50,001 bytes | 66,513 bytes |
| Darwin code section | 26,044 bytes | 28,272 bytes |
| Darwin unwind section | 4,060 bytes | 4,676 bytes |
| Darwin data section | 4,376 bytes | 4,400 bytes |
| Core WASM | 15,546 bytes | 16,914 bytes |

The native code increase is 2,228 bytes; the text segment grows from 32,768
to 49,152 bytes as checked decoding and error cleanup cross its alignment
boundary. Unwind data grows by 616 bytes and data by 24 bytes. No size
baseline changed, and no timing improvement is claimed.

The final native `tsort` executable remains 149,329 bytes. Its code section
decreases from 109,280 to 107,904 bytes; unwind data increases from 14,572
to 15,028 bytes, and the data section remains 6,328 bytes.

## Native tsort comparison

Measured on arm64 macOS with the preceding stage-2 compiler for both Fern versions,
GNU coreutils 9.12 and Rust uutils 0.0.29. The pipeline first checks 1,000
pairs, then changes only the pair count to 100,000. Each workload has two
warmup rounds and seven timed rounds with alternating command order; every
output is checked. Sanitizers and other local compiler jobs are absent.

Rebuilding both executables with the refreshed compiler produces identical
bytes, so the measurements still apply. The executable SHA-256 hashes are
`10b28d2458c8e188f63f6d5f8c867ee856dc2629c8be059fab80d92cc8b763a6`
before and
`83b2d4b96634a7b345429d29176d62c86c54f9043df8014f941c534452d16018`
after.

| Workload | Previous Fern | Byte-based Fern | GNU | uutils |
| --- | ---: | ---: | ---: | ---: |
| Reversed chain, median | 25.802 ms | 25.423 ms | 48.580 ms | 145.116 ms |
| Independent names, median | 29.108 ms | 26.752 ms | 46.376 ms | 137.543 ms |
| Chain, peak RSS | 30,310,400 B | 34,390,016 B | 10,944,512 B | 28,459,008 B |
| Independent names, peak RSS | 29,556,736 B | 33,587,200 B | 9,306,112 B | 22,118,400 B |

The before/after timing ranges overlap, so these samples do not establish a
Fern speed improvement. Chain timings range from 25.242 to 27.410 ms before
and 24.673 to 27.871 ms after; independent-name timings range from 29.007
to 46.153 ms before and 26.418 to 29.031 ms after.

The raw representation retains the input and stores
name spans and collision links; peak RSS increases by about 4 MB in these
workloads. The initial record-key index used the native linear-map path and
was rejected after its 1,000-pair pilot showed a slowdown. The final integer
index uses the hash-table path on both native and WASM targets.
