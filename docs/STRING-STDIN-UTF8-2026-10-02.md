# Whole-input bytes and checked stdin text

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

On the current integration with main through `0d7a8d321`:

- Partial-input I/O fault tests pass for valid, malformed and multi-chunk
  prefixes. The bootstrap target matrix passes in 11.805 seconds.
- Primary native/core-WASM, interpreter, raw-reader and example-tee tests
  pass in 103.266 seconds, including ownership checks. All lint gates pass.
- The actual producer stage-2 compiler passes 96 additional text cases:
  three APIs, 16 inputs each, on Darwin and core WASM. All allocation and
  free counts balance. Cases include NUL, empty input, malformed sequences,
  truncated final input, and every split of two-, three- and four-byte scalars.

Compiler-driver regressions pass in 164.169 seconds, and all nine browser
shim tests pass. The expanded GNU `env`/`tsort` parity suite passes in 1.139
seconds, including binary names from files and stdin, cycles, NUL truncation,
unsigned byte ordering and distinct names with the same FNV hash. Primary
`tsort` and explicit map-key regressions pass on native and core-WASM targets
with balanced ownership. The compiler also rejects no extra target effects
from an unused hash implementation. All lint gates pass after those changes.

The published-seed bootstrap completes in 25, 23 and 24 seconds. Stages two
and three are identical: 12,114,209 bytes, SHA-256
`5a58b562dec1e990b3ce84a066941f59b878a7b6834133751d1c37202459338c`.
The final stage-2 compiler repeats all 96 text cases and 16 `tsort` cases on
Darwin/core WASM with balanced ownership. The final integer-index `tsort`
also passes the GNU parity and primary target suites, all lint gates, and
16 actual stage-2 Darwin/core-WASM cases with balanced ownership. The full
unit suite and all lint gates pass on the final integration. Validation
durations are not performance comparisons.

Bootstrap Preview 2 tests pass. Primary Preview 2 stdin remains unsupported:
the unchanged compiler refuses both the old `read_chunk` and the new
`read_chunk_bytes` because its component framing has no Reader imports.
Primary core-WASM results do not establish Preview 2 support. Adding that
framing remains separate target work.

## Size

The same stage-2 compiler built a program that reads stdin and prints its
byte length, using the old and new stdlib respectively. Empty, multi-chunk
ASCII and Unicode inputs produced the expected length on both versions.

| Artifact | Before | After |
| --- | ---: | ---: |
| Darwin executable | 50,001 bytes | 66,513 bytes |
| Darwin code section | 26,044 bytes | 27,836 bytes |
| Darwin unwind section | 4,060 bytes | 4,612 bytes |
| Darwin data section | 4,376 bytes | 4,400 bytes |
| Core WASM | 16,410 bytes | 17,870 bytes |

The native code increase is 1,792 bytes; the executable crosses a 16 KiB
text-segment boundary. Symbol attribution using platform-assembled objects
accounts for 1,784 bytes: UTF-8 validation adds 1,412, raw collection helpers
add 908, Result cleanup adds 700, and caller/integer-formatting changes add
24. Replacing the old collector and join helpers saves 1,260 bytes. The
native assembler's code-size delta is eight bytes larger than the object
comparison. No size baseline changed, and no timing improvement is claimed.

The final native `tsort` executable remains 149,329 bytes. Its code section
decreases from 110,224 to 108,420 bytes; unwind data increases from 15,060
to 15,452 bytes, and the data section remains 6,328 bytes.

## Native tsort comparison

Measured on arm64 macOS with the same final compiler for both Fern versions,
GNU coreutils 9.12 and Rust uutils 0.0.29. The pipeline first checks 1,000
pairs, then changes only the pair count to 100,000. Each workload has two
warmup rounds and seven timed rounds with alternating command order; every
output is checked. Sanitizers and other local compiler jobs are absent.

| Workload | Previous Fern | Byte-based Fern | GNU | uutils |
| --- | ---: | ---: | ---: | ---: |
| Reversed chain, median | 26.094 ms | 25.456 ms | 49.352 ms | 146.139 ms |
| Independent names, median | 29.599 ms | 27.245 ms | 46.421 ms | 136.790 ms |
| Chain, peak RSS | 30,310,400 B | 34,357,248 B | 10,928,128 B | 28,442,624 B |
| Independent names, peak RSS | 29,556,736 B | 33,554,432 B | 9,289,728 B | 22,069,248 B |

The before/after timing ranges overlap, so these samples do not establish a
Fern speed improvement. Chain timings range from 25.297 to 26.807 ms before
and 24.876 to 26.671 ms after; independent-name timings range from 29.288
to 30.026 ms before and 26.484 to 47.008 ms after.

The raw representation retains the input and stores
name spans and collision links; peak RSS increases by about 4 MB in these
workloads. The initial record-key index used the native linear-map path and
was rejected after its 1,000-pair pilot showed a slowdown. The final integer
index uses the hash-table path on both native and WASM targets.
