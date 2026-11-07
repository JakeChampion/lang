# Restricted PNG codec

The PNG module decodes grayscale at one, two, four or eight bits per sample without interlace.
It also decodes eight-bit grayscale with alpha, RGB and RGBA.
Indexed input supports one, two, four and eight-bit indices with PLTE and optional indexed tRNS.
Grayscale and RGB tRNS produce RGBA8 with exact sample-key comparisons before expansion.

Packed samples use exact full-range expansion into RGB8 after byte-based filter reconstruction.
Unused row padding does not produce pixels. Its bits remain part of filter dependencies.
It uses Fern-native CRC-32, DEFLATE, and Adler-32 from the standard library.

It does not call an external image codec.

The [sequential row interface](png-rows.md) can emit selected rows without a complete decoded pixel array.
It still materializes the entire filtered frame before row callbacks.

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

Interlaced and sixteen-bit images return Unsupported.
Grayscale and indexed input admit one, two and four-bit samples.
Unknown critical chunks, animation and nontrivial color metadata also return Unsupported.

This includes ICC, gamma, and chromaticity chunks.
Other ancillary chunks do not survive a decode and encode cycle.

The decoder preserves raw channel bytes without a color-profile conversion.
It expands grayscale to equal red, green and blue samples in RGB8 output.
Grayscale with alpha produces RGBA8 and preserves the explicit alpha sample.
The filter predictors use the encoded one-byte or two-byte pixel width before expansion.

Transparent pixels retain their original gray sample.
The caller must use the documented sRGB interpretation only for compatible input.

There is no general PNG color-management claim.

Indexed images require one PLTE before IDAT. Indexed tRNS must follow PLTE and precede IDAT.
The decoder rejects duplicate palettes, excess entries, invalid alpha lengths and out-of-range pixel indices.

RGB8 output retains exact palette samples. A tRNS chunk selects RGBA8 output.
Missing alpha entries are opaque, and transparent pixels retain their palette colors.
An optional suggested palette on RGB/RGBA input does not change its samples.

Grayscale tRNS requires two bytes. Truecolor tRNS requires six bytes.
The decoder masks unused key bits and retains colors under zero alpha.

The decoder rejects duplicate tRNS, tRNS after IDAT, PLTE after tRNS and tRNS on explicit alpha formats.
Fixed key metadata adds no retained sample array. Row plans charge expanded RGBA rows through existing limits.

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

Fixtures reject malformed transparency, unsupported profiles, animation, unknown critical chunks and incorrect scanline length.
Python zlib generated the fixture streams offline. It is not a runtime dependency.

The fixture generator resides beside the Fern fixture module.

A separate Fern executable writes an encoded RGBA fixture.
An independent Python check verified its chunk CRCs and decompressed scanline bytes.

The native suite uses heap sanitization and reports zero live bytes.
The [finite differential and mutation corpus](png-qualification.md) adds 964 cases against an offline Pillow reference.
Broad compatibility, unbounded fuzz campaigns, peak RSS, and production CPU deadlines remain unverified.

This codec is experimental and has no production-readiness claim.

The native compiler must contain the static-append allocation fix from PR #13208.
The original compiler corrupts a fixture after an append and subsequent allocation.

Do not change codec code to bypass that defect.
PR #13208 and the PNG implementation PR #13210 merged.
Their original CI runs did not establish a full successful validation.
The release gate still requires actual current-head CI and the remaining qualification work.
