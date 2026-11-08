# JPEG XL finite F16 fields

**Status:** Locally qualified. Publication remains subject to the image PR gate.
**Context:** Fern-native image engine.
**Date:** 2026-11-08.

## Scope

Extend the qualified raw bit reader with finite F16 metadata fields.
Implement both directions in pure Fern.
These fields supply later color, extra-channel and frame metadata.
They do not set the numeric policy for raw image samples.
Full pixel, conversion, frame and container support remains open.

## Contracts

Readers borrow bytes and accept a position and closed logical extent.
They return the exact binary32 value and next position.
They retain signed zero and subnormal values.
They reject F16 infinity and NaN declarations.
They reject truncated fields before access beyond the extent.

Writers return one raw field for the qualified byte writer.
They reject nonfinite values and values outside the reference finite range before output.
They reproduce the original metadata coder's truncation and tiny-value normalization.
Do not substitute a generic nearest-even conversion.
Raw sample words retain all their bits under their separate contract.
The public image model and its NaN policy remain independent.

## Validation

Compile original pinned F16 coders and their byte writer as offline references.
Check every input word and exact binary32 result.
Check signed zeros, exponent transitions, subnormals, boundary neighbors, NaN and infinity.
Check canonical output, all bit truncations, logical bounds and output budgets.
Run a small complete pipeline before each full campaign.
Run native, actual Wasm and applicable Linux checks with exact source and heap evidence.

The independent value model uses Python's half-float format and ordered finite values.
It does not copy the Fern bit shifts.
The bidirectional corpus covers every half word and binary32 interval boundaries.
Five original encoded images supply intensity, minimum luminance and linear-threshold fields.
Their basic information, exact bytes, source hashes and resolved reference dependencies remain frozen.

The routine CI lane compiles original source coders and uses these frozen images.
It requires no runtime codec library.

Reference sources: [field readers](https://github.com/libjxl/libjxl/blob/v0.12.0/lib/jxl/fields.cc)
and [field writers](https://github.com/libjxl/libjxl/blob/v0.12.0/lib/jxl/enc_fields.cc).
This stage makes no complete-conformance, physical-memory or performance-rank claim.
