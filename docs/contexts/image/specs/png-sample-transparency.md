# PNG sample transparency

**Status:** In progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

PNG can select one transparent grayscale sample or RGB color through tRNS.
The current decoder rejects these images despite support for their sample formats.

## Intended behavior

Decode grayscale tRNS at depths one, two, four and eight, plus truecolor tRNS at depth eight.
Return the existing RGBA8 model through eager, sequential, incremental and provider interfaces.
Match original samples before expansion. Retain RGB values under zero alpha.
Mask unused key bits as PNG third edition requires.

## Implementation decisions

- Keep transparency keys as fixed metadata without a retained byte array.
- Reuse encoded-byte filters and bounded row expansion.
- Require two bytes for grayscale keys and six bytes for RGB keys.
- Reject duplicate tRNS, tRNS after IDAT and PLTE after tRNS.
- Reject tRNS with explicit alpha channels.
- Charge output expansion through existing pixel and row limits.
- Keep all runtime code in Fern. Use libpng and Pillow as offline references only.

## Validation

Check exact pixels with independent filters, Pillow and libpng.
Cover all admitted depths, key extremes, partial RGB matches and nonzero unused key bits.
Test every filter, packed padding, reader boundaries, aliases, cancellation and exact admission.
Require the same errors across interfaces and no consumer callbacks for invalid streams.
Retain checksum error precedence. Run native, Wasm and applicable Linux correctness checks.

## Remaining scope

This stage does not complete PNG. Sixteen-bit precision, Adam7, indexed output and wider metadata remain open.
The encoder continues to emit RGB8/RGBA8. Full decode and encode remain the completion requirement.

## Reference

[PNG third edition, tRNS](https://www.w3.org/TR/2025/REC-png-3-20250624/#11tRNS) defines sample keys and unused-bit masks.
