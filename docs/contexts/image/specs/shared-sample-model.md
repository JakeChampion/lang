# Shared integer and floating-point image samples

**Status:** Draft. Shared-model architecture confirmed.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The current image model contains RGB8/RGBA8 storage and byte-valued pixels.
It cannot preserve sixteen-bit PNG samples or wider JPEG XL image values.
Several consumers equate channel count with byte count per pixel.
An unqualified format extension can corrupt data in those consumers.

## Solution

Extend the shared pixel model with explicit integer and floating-point sample formats.
Retain the existing RGB8/RGBA8 APIs and their current results.
Separate channel count, stored byte width and sample precision.
Preserve exact samples until the caller explicitly requests a conversion.
Keep all runtime implementations in Fern.

## User stories

1. A PNG caller can retain all sixteen bits of every color and alpha sample.
2. A JPEG XL caller can retain sample precision and distinct extra channels.
3. An existing RGB8/RGBA8 caller keeps its current storage and API behavior.
4. A caller can select an explicit conversion before a lower-precision operation.
5. Malformed layouts fail before indexing or allocation.

## Implementation decisions

The user confirmed the shared-model extension on 2026-11-07.
The first prerequisite provides exact byte access to shared sample words.
Its byte order is explicit. It preserves raw integer and floating-point bits.
It validates word width, offset and integer fit before a write.
Owned writes preserve existing aliases through normal copy-on-write semantics.

The model must distinguish sample precision from its storage container.
Channel metadata must retain alpha association and distinct extra-channel precision.
Floating-point storage must retain values outside the unit interval for HDR and wide gamut.
Storage access must not apply color transforms, clipping or gamma correction.
Numerical conversion and display transforms need separate explicit contracts.

Audit all byte-only consumers before wider images reach them.
Keep current byte limits until measurements justify a change.
Account for storage width in every row, image, crop, cache and scratch calculation.
Check forged public layouts before access. Preserve byte-padding and alias behavior.
The final model must support the complete format requirements, including wider frames and metadata.

## Reference boundaries

The [PNG third edition](https://www.w3.org/TR/png-3/) defines sixteen-bit samples and exact transparency comparisons.
The [libjxl API](https://libjxl.github.io/libjxl/api_common.html) distinguishes stored type, byte order and source precision.
Its floating output can retain values outside the unit interval.
Its API is implementation evidence, not a substitute for ISO/IEC 18181.
Retain a normative JPEG XL precision audit before a conformance claim.

## Testing strategy

Test exact byte access separately from numerical conversion.
Use independent integer byte references for both byte orders.
Include the complete sixteen-bit domain and larger-word boundaries.
Test malformed offsets, widths, lengths, values, forged layouts and caller aliases.

Verify unchanged byte APIs and every direct consumer that wider formats affect.
Use independent PNG/JPEG XL references only as offline tools.
Run applicable native, Wasm and Linux checks with heap evidence.

## Out of scope for the prerequisite

The sample-word primitive does not decode images or select a public conversion policy.
It does not provide full floating-point arithmetic, color management, animation or metadata support.
Those requirements remain in the full image workstream.
No throughput or physical memory claim applies.

## Open questions

For wider input, must existing byte-valued helpers reject the format until an explicit conversion occurs?
The proposed rule preserves precision and exposes unsupported operations before data loss.
Exact public format variants, float conversion rules and channel metadata remain design work.
