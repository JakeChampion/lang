# PNG packed grayscale input

**Status:** In progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Intended behavior

Accept noninterlaced grayscale with one, two or four bits per sample.
Return RGB8 with equal channels and exact full-range linear sample expansion.
Preserve the current eight-bit grayscale and alpha behavior.
Apply the extension to eager, sequential, incremental and provider paths.

## Implementation decisions

The private scanline description retains sample depth and encoded byte length.
Reconstruct filters on packed bytes with a one-byte left dependency.
Extract each sample from the most significant available bits after reconstruction.
Ignore unused low bits in the last byte for output samples.
Retain the complete reconstructed byte for the next row's filter dependency.

Compute encoded row length with wide arithmetic after checked output admission.
Keep logical output, scratch and work budgets separate from encoded storage.
The public pixel and codec interfaces remain compatible.
All runtime work remains pure Fern.

## Validation

Compare all legal sample values and byte boundaries against independent fixtures and Pillow.
Use all five filters and nonzero unused bits.
Verify exact expanded limits, row budgets, aliases and cancellation.
Reject invalid color/depth combinations and malformed streams before callbacks.
Run native and Wasm image suites with heap checks.

## Remaining scope

Packed grayscale output, indexed color, sixteen-bit samples and Adam7 remain future checkpoints.
Metadata, color profiles, transparency keys and animation also remain open.
The complete PNG decode and encode requirement remains active.
No performance claim follows from these correctness tests.

## Reference

[PNG third edition](https://www.w3.org/TR/2025/REC-png-3-20250624/), sections 7.2, 9.2, 11.2.1 and 13.12.
