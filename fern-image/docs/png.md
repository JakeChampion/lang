# Restricted PNG codec

The PNG module supports eight-bit RGB and RGBA without interlace.
It uses Fern-native CRC-32, DEFLATE, and Adler-32 from the standard library.

It does not call an external image codec.

## Decode contract

The decoder validates the signature, chunk lengths, CRCs, and chunk type letters.
IHDR must be first and unique. IEND must be last and empty.

IDAT chunks must be consecutive. Their bytes form one strict zlib frame.
The zlib adapter accepts a 32 KiB window without a preset dictionary.

It rejects bad headers, checksums, trailing compressed bytes, and oversized output.

The decoder reconstructs all five PNG scanline filters, with byte-width modular addition.
The pixel constructor validates dimensions, stride, pixel count, and output bytes before decompression.

The exact filtered length must match height times the sum of row bytes and one filter byte.
A wrong length or filter value produces an explicit error.

Input bytes cannot exceed 16 MiB. A file cannot contain more than 1,024 chunks.
Filtered bytes cannot exceed either the caller byte limit or 64 MiB.

The decoded pixel array also obeys pixel package limits.
These are separate arrays. They do not establish an aggregate process memory budget.

## Compatibility limits

Indexed, grayscale, interlaced, and non-eight-bit images return Unsupported.
Unknown critical chunks, tRNS transparency, animation, and nontrivial color metadata also return Unsupported.

This includes ICC, gamma, and chromaticity chunks.
Other ancillary chunks do not survive a decode and encode cycle.

The decoder preserves raw channel bytes without a color-profile conversion.
The caller must use the documented sRGB interpretation only for compatible input.

There is no general PNG color-management claim.

## Encode contract

The encoder validates source storage with the pixel access validator.
It removes row padding and emits filter zero for each row.

It emits IHDR, one IDAT, and IEND with CRCs and a zlib Adler-32 trailer.
It strips metadata. It preserves transparent RGB values in RGBA8.

Filtered input and encoded output cannot exceed 16 MiB.
The caller also supplies a positive encoded-byte limit.

A failure returns an explicit error rather than partial output.

## Verification and release gate

Seven native cases cover independent RGB/RGBA goldens for all five filters.
They also cover round trips, every truncated prefix, CRC and length failures, invalid filters, and resource budgets.

Fixtures reject transparency, profiles, animation, unknown critical chunks, and incorrect scanline length.
Python zlib generated the fixture streams offline. It is not a runtime dependency.

The fixture generator resides beside the Fern fixture module.

A separate Fern executable writes an encoded RGBA fixture.
An independent Python check verified its chunk CRCs and decompressed scanline bytes.

The native suite uses heap sanitization and reports zero live bytes.
Fuzz campaigns, broad differential compatibility, peak RSS, and CPU deadlines remain unverified.

This codec is experimental and has no production-readiness claim.

The native compiler must contain the static-append allocation fix from PR #13208.
The original compiler corrupts a fixture after an append and subsequent allocation.

Do not change codec code to bypass that defect.
Keep this change in draft until that prerequisite merges and CI passes.
