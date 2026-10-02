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

Compiler-driver/browser regressions, refreshed bootstrap reproducibility
and the full unit gate remain pending. Validation durations are not
performance comparisons.

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
