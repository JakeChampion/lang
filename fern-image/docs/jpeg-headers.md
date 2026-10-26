# Baseline JPEG header inspection

Issue #13562 is a native child of JPEG milestone #13216.
This slice parses a restricted header profile in Fern.
It does not decode JPEG pixels, validate entropy data, or establish JPEG compatibility.
M3, M4, and M5 acceptance remain separate.

## Reference and profile

The syntax reference is [ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), Annex B and Annex C.
The downloaded PDF has SHA256 `631031d4ba56b06abee3e312a0f235b9422da9c7267d1c8f7604418795768bf0`.
Sections B.2.2 through B.2.4 specify frame, scan, and table syntax.
Annex C prohibits an all-ones Huffman code.
The parser enforces that rule through the available canonical code space at each length.

The accepted frame uses SOF0, eight-bit samples, and one or three distinct components.
Sampling factors range from one through four.
An interleaved scan contains at most ten data units per MCU.
The first scan must contain every frame component exactly once, in frame order.
Its spectral range is zero through 63, with no successive approximation.
Zero-height frames with DNL, partial component scans, progressive coding, arithmetic coding, and other frame modes return explicit errors.

Quantizers use eight-bit nonzero values and destinations zero through three.
Returned values retain their transmitted zig-zag order.
Baseline Huffman destinations are zero and one for each class.
The parser rejects empty or oversubscribed tables, all-ones codes, and more than 256 symbols per table.
It accepts only baseline DC categories and AC run/size symbols.
Definitions can replace an earlier table before the first scan.

The scan must reference tables present in the same input.
External abbreviated table streams are unsupported.
The parser validates the restart-interval field but does not inspect restart markers in entropy data.
APP and COM payloads remain opaque and contribute to the metadata-byte count.
The parser does not interpret JFIF color meaning, EXIF orientation, Adobe transforms, or ICC profiles.

## API and limits

Import `packages/codecs/jpeg/header` and call `parse(input, limits, cancelled)`.
Input is a borrowed byte view.
The result owns its component descriptors, quantizer values, and Huffman counts and symbols.
No result field contains an input view or opaque metadata payload.
The `entropy_offset` identifies the byte after the first scan header.

The result can succeed when input ends exactly at that offset.
It can also succeed when later entropy bytes are malformed or EOI is missing.
Callers must not treat header success as a complete-image validity result.
Future decoders must validate their own input and must not trust caller-constructed public header records.

`output_layout` projects a tightly packed RGB8 result for resource admission.
It does not represent decoded storage or prove a color conversion.
Grayscale uses the same three-byte projection.
The pixel layer checks dimensions, multiplication, pixel count, decoded bytes, and its 64 MiB hard byte cap.

| Limit | Default | Hard cap |
| --- | ---: | ---: |
| Supplied input bytes | 16 MiB | 16 MiB |
| Header extent through the first SOS | 1 MiB | 1 MiB |
| Header markers after SOI | 1024 | 4096 |
| Projected pixels | 16777216 | Checked layout arithmetic |
| Projected RGB8 bytes | 64 MiB | 64 MiB |

Marker fill bytes count toward header extent, but not the marker count.
Segment lengths include their two length bytes, as T.81 specifies.
The parser rejects out-of-range extents before payload reads.
The segment-count limit includes tables, metadata, frame, restart interval, and scan headers.
The input-byte limit covers the entire supplied view, even though parsing stops before entropy data.

Cancellation checks occur before parsing, at marker boundaries and fill bytes, between tables, and during Huffman symbol copies.
Fixed loops cover at most 64 quantizers, 16 code counts, or three frame components between their surrounding checks.
A callback remains trusted code whose own CPU or allocation cost is unmetered.
These limits do not bound allocator capacity, compiler temporaries, callback work, process memory, or elapsed time.

## Fixtures and validation

The offline generator uses pinned Pillow 12.3.0 with libjpeg-turbo 3.1.4.1.
It creates finite patterns, then loads each complete JPEG through that reference implementation.
The manifest records source hashes, dimensions, component sampling, library versions, generator hash, and entropy offsets.
Six fixtures cover grayscale, 4:4:4, 4:2:2, 4:2:0, and optimized sparse Huffman tables.
Two genuine fixtures exercise explicit progressive and CMYK rejection.
A separate synthetic header contains no entropy data or EOI and tests the API boundary.

The 14 TAP cases cover every prefix before selected scan headers, malformed fields and tables, table replacement, and component identifiers.
They also cover exact budgets, hard caps, marker fill, opaque metadata, restart interval, offset views, ownership, and every cancellation poll.

Scan components must follow their positions in the frame header, as T.81 A.2 and B.2.3 require.
Identifiers can have any distinct byte values and need not increase numerically.
All five invalid permutations of three components are regression cases.

Native Linux and ARM64/QEMU10 pass with heap sanitization and zero final live bytes.
Wasmtime passes with balanced leak census and zero final live bytes.
The original 13-case campaign used compiler `005505da83fd`, SHA256 `cbdec0dc8539c5b6fa4426102bba7d3721a8edb03ccf85c714de789accc045e1`.
Current-head CI remains a separate validation result.

The scan-order repair passes all 14 cases on native Linux, Wasmtime, and ARM64/QEMU10 with zero final live bytes.
That campaign uses compiler `3bebeb4b6fa5`, SHA256 `59e020168d59e830c7eecc6b38abb8ff1c2731d12e0b391a5f1ac0fd11aad8ee`.
The complete native and Wasm image scripts each pass all 150 TAP cases and their smoke cases with that fixed compiler binary.
All 61 native and 37 Wasm worker censuses are balanced with zero final live bytes.

The native and Wasm image scripts include these cases.
Reproduce fixture generation with the existing pinned offline reference environment:

```sh
python3 -m pip install --require-hashes -r fern-image/tests/requirements-reference.txt
python3 fern-image/tests/generate_jpeg_headers.py --check
```

The reference library is a test tool, not a runtime codec dependency.
Generated patterns do not replace photographs, broad interoperability tests, unbounded fuzzing, or security qualification.
Header observations establish no pixel quality, throughput parity, physical memory bound, or production safety.

## Remaining JPEG work

Native entropy decoding, coefficient checks, IDCT, dequantization, and chroma reconstruction remain unimplemented.
Restart sequences and final image framing need end-to-end validation.
Color interpretation, EXIF orientation, ICC policy, malformed entropy corpora, and representative photos need independent work.
Baseline decoding must precede encoder, DCT, quality-tuning, or progressive work.
