# Baseline JPEG encoder

**Status:** In progress. Image API remains a draft.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The image engine has a restricted baseline JPEG decoder and no JPEG writer.
Programs cannot produce JPEG images through the pure Fern image engine.
The user requires decode and encode for JPEG without external runtime codecs.

## Intended behavior

Add a bounded baseline JPEG writer in dependency-safe stages.
Retain the current checked pixel model and caller ownership.
Use explicit color and alpha policies.
Validate output with independent JPEG decoders and malformed-input controls.

## First internal stage

The scalar forward transform accepts one complete eight-bit component block.
It applies the level shift and returns 64 binary64 coefficients in natural order.
The output index combines the vertical frequency and horizontal frequency.
The shared transform basis retains the current inverse transform's exact constants.
This stage does not choose a quality scale or an image-level API.

The transform rejects incorrect input lengths before sample access.
It checks cancellation before work, during each separable pass and before success.
Each call owns its result and preserves input aliases.
Constant blocks produce exact DC coefficients and zero AC coefficients.
No partial result escapes on cancellation.

## Subsequent stages

The internal quantizer takes natural-order binary64 coefficients and transmitted-order eight-bit quantizers.
It returns natural-order integers for the existing inverse transform.
Every quantizer must be nonzero.
Round each binary64 quotient to the nearest integer, with exact ties away from zero.
Reject nonfinite input and results outside the existing inverse transform's signed sixteen-bit coefficient domain.

Keep the shared zig-zag permutation identical to the existing decoder table.
Test adjacent representable values around ties, every quantizer and the coefficient boundaries.

This numeric rule defines the internal stage, without a quality scale or image-level API.
Add baseline DC differences, AC run lengths, canonical Huffman output and byte stuffing.
Specify color-plane conversion, edge extension, sample factors and alpha admission before the image writer.
Preserve exact output-byte limits, dimensions, cancellation and fresh call state.

The internal MCU geometry prerequisite accepts explicit sample factors for a complete frame in one scan.
It retains component dimensions, raster order, single-component MCU rules and complete dummy blocks.
The public image writer still needs its own option and alpha contracts.

## Validation

The internal entropy stage accepts complete natural-order blocks for one component.
It uses explicit canonical DC and AC tables and an optional restart interval.
Reject duplicate symbols, invalid table shapes, oversubscribed trees and all-ones codes.
Reject DC coefficients outside signed eleven-bit precision and AC magnitudes above 1023.
The output contains entropy bytes and restart markers, without frame headers or EOI.

Use one-bits for alignment and stuff zero after each entropy byte of value 255.
Count stuffed bytes and restart markers against the caller's exact output limit.
Reset DC prediction at restart boundaries and cycle the eight restart marker values.
Preserve input aliases and reject malformed shapes before cancellation callbacks.

Check cancellation during table preparation, blocks and output, and before success.
Compare exact bytes with an independent reference and complete arrays with libjpeg.

Test all uniform byte values, individual sample impulses and directional patterns.
Use an independent high-precision direct sum for forward coefficients.
Retain the inverse transform's existing mathematical tolerance for the first finite numeric campaign.
Compare complete coefficient arrays, without a larger tolerance after a failure.

Test invalid shapes, borrowed windows, caller aliases and every cancellation checkpoint.
Re-run existing inverse-DCT qualification after the shared-basis change.
Run native and Wasm tests with their applicable heap checks.

No test establishes a universal error bound or JPEG precision certification.
The complete writer requires separate end-to-end qualification.

## Open API decisions

The first image writer's quality options, sample factors and RGBA admission remain unspecified.
Confirm substantial choices through the spec workflow before their implementation.
Always pure Fern and both decode and encode are confirmed requirements.

## Reference

[ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), Annex A.3.
