# Native compression feasibility

## Current qualification

The [2026-11-01 qualification](../../docs/benchmarks/png-compression-qualification-2026-11-01/README.md)
records the current native Fern implementation for #13221.
It preserves the original stored, fixed-Huffman, and dynamic-Huffman goldens.
New strict-adapter tests cover valid-checksum headers with unsupported windows,
methods, and dictionaries, trailing data, concatenated streams, checksums,
exact output limits, and every truncated prefix of a stored frame.
The native and Wasm image scripts run these tests.

The twelve sanitized processes pass 56 TAP cases across ARM64 Darwin,
ARM64 Linux, x86 Linux under QEMU, and Wasm.
Every process has nonzero balanced allocations and zero final live bytes.
Wasm sanitization records leaks; it does not detect over-release or use-after-free.
These selected tests do not establish CPU deadlines, peak or aggregate memory
bounds, exhaustive fuzz coverage, or production compatibility.
The full CI gate remains mandatory before the qualification PR merges.

## Original audit scope

The rebuilt Fern compiler supports `std/deflate` without external codec libraries.
Three native probe cases pass with the heap sanitizer and zero live bytes.
The probe tests stored, fixed Huffman, and dynamic Huffman blocks inside zlib frames.
It checks known plaintext, exact output limits, truncation, header errors, and Adler-32 failures.

The golden fixture payload is `Fern PNG compression foundation. `.
The stored fixture contains one copy. The other fixtures contain 128 copies.
Python zlib generated the test data offline with levels zero and six.
The fixed fixture uses `Z_FIXED`. The dynamic fixture uses `Z_DEFAULT_STRATEGY`.

Python is not a runtime dependency of the Fern decoder.

The decoded lengths are 33 and 4,224 bytes.
The dynamic fixture succeeds with an output limit of 4,224.
It returns `OutputLimit` with limits of 4,223 and zero.
These checks verify selected limit behavior. They do not establish a complete security audit.

## PNG integration requirements

The current zlib wrapper permits bytes after its checksum trailer.
PNG integration must verify exact compressed stream consumption.
Use `inflate_from` and its consumed count in a strict PNG framing adapter.
Do not change a general wrapper contract solely to satisfy PNG policy.
Validate the zlib method, window, dictionary flag, header check, and Adler-32 trailer.
Limit compressed input and decompressed output independently.

The current inflater also accepts distances according to its DEFLATE implementation.
No exhaustive window-size or malformed-Huffman compatibility audit ran here.
CPU limits, cancellation, aggregate memory, fuzz coverage, and peak RSS remain unverified.
The next PNG change must address these limits before production use.

No PNG chunk parser, scanline reconstruction, decoder, or encoder exists in this audit.
The next implementation can use the verified native compression component.
It must retain strict signature, chunk order, CRC, length, and scanline checks.
Interlaced, indexed, and non-eight-bit images require explicit unsupported errors in the first decoder.
