# PNG grayscale input

**Status:** In progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The PNG decoder rejects grayscale files even when their samples fit the current pixel formats.
Its filter logic uses the output channel count as the encoded channel count.
Grayscale requires separate encoded rows and output rows.

## Intended behavior

Decode noninterlaced eight-bit grayscale and grayscale with alpha.
Expand each gray sample into equal red, green and blue samples.
Preserve each alpha sample and the gray value under zero alpha.
Return the current checked RGB8 or RGBA8 image type.
Apply this rule to eager decode, sequential rows, incremental rows and graph providers.
Keep the encoder's RGB8/RGBA8 output contract.

## Implementation decisions

The PNG codec retains a private description of the encoded scanline.
Its filter byte distance and row length refer to encoded samples.
Pixel admission refers to expanded output storage.
Filter validation covers the entire file before row callbacks.
Each row session retains encoded filter dependencies and returns an owned expanded row.

Current RGB/RGBA budget values remain unchanged.
Grayscale plans charge encoded scratch plus the expanded row and its expansion work.
The row charge also covers the graph adapter's two expanded rows when that allowance is greater.
These logical charges do not establish a physical memory bound.

This extends the current modules and APIs without a new public image type.
The pixels, compression and graph interfaces retain their current contracts.
All runtime codec work remains pure Fern.

## Validation

Use independent PNG filter equations and Pillow as offline references.
Compare exact bytes through eager, sequential and incremental decode.
Cover all five filters, row boundaries, narrow rows and alpha changes.
Test exact admission limits and output expansion beyond the caller byte limit.
Reject malformed lengths, checksums, filters and streams without callbacks.

Test cancellation, repeated row access and retained row aliases.
Re-encode expanded images and compare decoded samples.
Run the current PNG and graph tests on native and Wasm targets.

## Out of scope

Packed samples, sixteen-bit samples, indexed color, Adam7 and tRNS remain unsupported.
Color profiles, animation and implicit color transforms retain their current restrictions.
This slice does not establish broad PNG conformance or production qualification.

## Reference

[W3C PNG specification](https://www.w3.org/TR/2025/REC-png-3-20250624/), sections 6.1, 7.2, 9 and 11.2.1.
