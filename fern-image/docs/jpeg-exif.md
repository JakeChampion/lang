# Exif primary-orientation metadata

`packages/codecs/jpeg/exif.fern` reads a standalone Exif APP1 payload.
The payload starts with the six bytes `Exif\0\0`, followed by a TIFF header and its directory data.
The caller excludes the JPEG marker and its two-byte segment length.
The implementation uses Fern without an external runtime codec dependency.

The API leaves the JFIF and RGB interfaces unchanged.
Those interfaces continue to reject Exif and ICC application metadata.
Exif can specify centered or co-sited component positions, so an orientation result does not establish a supported pixel profile.

## API

`exif.inspect(input, limits, cancelled)` returns `Result[ExifInfo, ExifError]`.
The input is a borrowed byte view.
`ExifInfo` contains byte order, visited directory count, total entry count, and `Option[Orientation]` for the primary image.
The result owns its scalar metadata and retains no input view.
Owned input updates through an alias retain normal copy-on-write behavior.

The eight orientation values are:

| Wire value | Result |
| --- | --- |
| 1 | `TopLeft` |
| 2 | `TopRight` |
| 3 | `BottomRight` |
| 4 | `BottomLeft` |
| 5 | `LeftTop` |
| 6 | `RightTop` |
| 7 | `RightBottom` |
| 8 | `LeftBottom` |

An absent primary tag returns `None`.
The interface does not substitute the specification default of 1.
A thumbnail orientation cannot replace the primary orientation.
The caller receives metadata only, without an image transform.

## Structure and limits

Both `II` and `MM` byte orders use TIFF marker 42.
All offsets start at the TIFF header, after the Exif prefix.
The reader visits IFD0 and at most one linked IFD1.
Visited directory tables must stay within the payload and cannot overlap or form a cycle.
An empty directory is accepted.
Tags must have numeric order without duplicates.

Orientation tag 274 must use SHORT type 3, count 1, and a value from 1 through 8.
Its two value bytes use the declared byte order and precede the unused inline bytes.
Other supported wire types are BYTE, ASCII, LONG, RATIONAL, UNDEFINED, SLONG, SRATIONAL, and UTF8 type 129.
The reader uses i64 arithmetic to check extents for unsigned 32-bit field counts and offsets.
String contents, rational meanings, and nested Exif, GPS, and MakerNote targets remain uninterpreted.
Unused payload bytes also remain uninterpreted.

`ExifLimits` contains positive `max_bytes` and `max_entries` values.
Defaults admit 65,533 payload bytes and 128 total entries across visited directories.
The absolute ceilings are 65,533 bytes and 4,096 total entries.
Larger caller limits retain these absolute ceilings.
Admission checks precede the first cancellation callback.
The entry allowance applies across both visited directories.

The byte limit measures the input view length.
The entry limit measures visited table entries.
These limits do not bound capacity, allocator storage, caller retention, RSS, process memory, or execution time.
The reader polls after admission, before each entry, before a second directory, and before success.
Cancellation is cooperative and provides no hard deadline.

## Errors and scope

`Signature`, `ByteOrder`, and `Version` identify unsupported headers.
`Truncated` identifies a short header or directory table.
`Directory` covers offset bounds, unsorted tags, table intersections, cycles, and a third linked directory.
`Duplicate` identifies repeated adjacent tags.
`Type` rejects a wire type outside the supported set.
`Value` rejects an empty field count, an invalid orientation, or an invalid out-of-line extent.

`Limit` reports invalid limits or an exceeded byte or total-entry allowance.
`Cancelled` reports an observed cancellation request.
Checks stop at the first error.
The reader returns no partial metadata on error or cancellation.

This slice does not parse a complete JPEG, apply orientation, decode thumbnails, implement ICC transforms, or validate all Exif semantics.
It makes no general compatibility, standards qualification, or production claim.
The format reference is CIPA DC-008-Translation-2026, Exif 3.1, sections 4.5.4, 4.6.2, and 4.6.5.1.6.
The official PDF SHA-256 is `9cc36399a46ab7aa4a65473bb8a4945d3b044da739b8f16a01b0be11886dcff9`.

## Finite qualification

The generator uses independent Python `struct` construction and explicit policy mutations.
Applicable offline Pillow 12.3 observations independently check primary orientation and absence.
Pillow does not register wire type 129.
Those vectors retain exact construction expectations and remain mandatory on all Fern targets.
An unavailable reference observation never counts as a pass.
The reference tools are test dependencies only.

Run `scripts/test-jpeg-exif.sh native`, `wasm`, or `arm64` with an absolute `FERN` path.
Set `PYTHON` to the pinned Pillow reference environment.
The worker checks every metadata field, error class, and declared cancellation poll exactly.
Every fresh worker must have a nonzero balanced heap census with zero live blocks.
The collector preserves failed inputs, output, and completed batches, and exits nonzero on failure.

The final campaign uses source `0d29d0af1531c69ba1f317be3808a50d257bb058`.
The clean compiler source is `ced67ee81f8c6cac337a214480c7ccb3be51be87`.
Its SHA-256 is `b8e32ee9becc7f4411929db85eb7d04221d4cb9e753a79246d22b4288f44e17f`.
Compiler and stdlib sources match the campaign checkout.
The standard two-stage build, bootstrap smoke, runtime audit, and both unchanged complexity gates pass.

The finite corpus has 532 cases with 481 distinct payloads.
These cases form 525 distinct combinations of payload, limits, and cancellation point.
Each target reports 295 successful results and 237 expected errors.
Pillow independently checks 262 applicable primary-orientation observations per target.
The generated bundle reproduces byte-for-byte with SHA-256 `15abe791171f4f143c9545482a27ccac87dff1223bcf8cbc66b78f59a72dcdea`.

Native, Wasm, and ARM64/QEMU10 each complete 67 fresh workers.
All 201 workers report nonzero balanced heap counts and zero live blocks.
Every target produces the same sample records.
All 1,244 native and 1,245 runner source/tool hashes remain unchanged during each campaign.
These include all 163 stdlib sources and all compiler Fern sources.

The expanded eight TAP groups and Go interpreter checks pass.
Native and ARM report 17,413 balanced allocations and frees, and Wasm reports 25,960, all with zero live blocks.
All 222 published TAP cases pass on the three targets.
The native and Wasm full scripts also pass their smoke, negative, and formatter probes.
Their 70 native and 46 Wasm heap censuses balance with zero live blocks.
The 31 standalone ARM executables also report balanced zero-live censuses.

All 1,283 regression source hashes and the compiler binary remain unchanged.
All 2,035 Fern format checks, build/vet, workflow lint, runtime bundle checks, test selectors, and eight protocol fault controls pass.
The ARM checks cover these standalone image programs, not the complete Go ARM harness.

Raw records are [native](../benchmarks/results/jpeg-exif-native.json),
[Wasm](../benchmarks/results/jpeg-exif-wasm.json), and
[ARM64](../benchmarks/results/jpeg-exif-arm64.json).
