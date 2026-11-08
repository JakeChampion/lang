# JPEG XL bitstream fields

**Status:** Locally qualified. Internal prerequisite only.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Scope

JPEG XL codestream fields use low-bit-first order within bytes.
The existing JPEG entropy module uses a different bit order and byte-stuffing rule.
This stage provides checked raw fields for later JPEG XL integer and header parsers.
Both read and write use pure Fern.
It introduces no public pixel, color or metadata policy.

## Contracts

The reader takes borrowed bytes, a bit position, a field width and a closed bit extent.
Positions and extents use signed 64-bit values to cover every supported byte-array length.
Each field contains zero through 56 bits. The reference reader has the same per-call width bound.
The reader checks the width, extent and requested end before access.
A zero-width field returns zero and preserves the position.

It must not read physical byte padding beyond the logical bit extent.
Alignment accepts only zero padding to the next byte boundary.

The writer takes explicit fields, an output-byte budget and a cancellation callback.
Each field value must fit its declared width, including zero-width fields.
Admission checks every field and the complete byte count before output allocation.

The writer returns owned bytes and the exact logical bit count.
High unused bits in the final byte are zero.
Cancellation exposes no partial result. Existing inputs and aliases retain their values.
The byte budget does not claim aggregate process memory or execution-time bounds.

## Validation

Compare exact fields and offsets with independent integer byte equations.
Use pinned libjxl 0.12.0 BitReader code only as an offline reference.
Cover every width and byte offset, empty fields, cross-byte reads and partial final bytes.
Reject invalid widths, positions, extents, values, nonzero padding and insufficient budgets.

Test every cancellation checkpoint and retained input aliases.
Run a small complete pipeline before the larger corpus.
Validate applicable native, Wasm and Linux execution with heap checks.

Integer distributions, bundle extensions, codestream headers and entropy coding remain later stages.
The complete JPEG XL scope includes Modular, VarDCT, frames, precision and all applicable container features.
That scope includes the edition-three HDR gain map box, jhgm, and its gain-map interpretation.
This checkpoint makes no complete-format or performance-rank claim.

References: [libjxl bit reader](https://github.com/libjxl/libjxl/blob/v0.12.0/lib/jxl/dec_bit_reader.h)
and [current file-format preview](https://www.iso.org/obp/ui?_escaped_fragment_=iso:std:iso-iec:18181:-2:ed-3:v1:en).

## Local evidence

The full image suites pass 474 native cases and 487 actual Wasm cases.
Both targets pass 1,596 reference cases after a 16-case campaign.
Each full campaign contains 1,409 exact accepted outputs and 33,788 logical queries.
The pinned reader confirms 4,112 packed fields and 33,077 physical-input fields per target.

Linux ARM64 and x86-QEMU each pass the seven new test groups.
All final heaps are positive, balanced and zero-live.
All 319 Python controls, source checks, lint-fast, formatter, type, strict module complexity, shell and workflow checks pass.
The compiler and stdlib retain the exact qualified primary source hashes.
Earlier image runtime and test sources retain their parent hashes.

The local receipt is image-jxl-bits-20261107/summary.json.
Reference receipts use the image-jxl-bits-native and image-jxl-bits-wasm prefixes.
This finite evidence does not establish complete JPEG XL conformance or a performance rank.
