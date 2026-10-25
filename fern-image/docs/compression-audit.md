# Native compression feasibility

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

The existing zlib wrapper permits bytes after its checksum trailer.
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
