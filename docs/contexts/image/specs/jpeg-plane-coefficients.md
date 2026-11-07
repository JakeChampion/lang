# JPEG component coefficients

**Status:** Internal prerequisite in progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The encoder primitives do not yet connect component planes to quantized blocks.
The encoder needs complete blocks in a defined order without a required image-sized coefficient array.

## Intended behavior

Fold one eight-bit component plane into natural-order quantized blocks.
The caller supplies the plane layout, edge policy, transmitted-order quantizers and block limit.
A consumer receives each owned block and its coordinates in plane row order.
Reuse the qualified block extractor, forward transform and quantizer.
Keep runtime code in Fern.

## Contracts

Validate layout, minimal storage, all quantizers and the complete block count before callbacks.
Use wide arithmetic for counts. Do not allocate a complete coefficient plane.
The block limit counts operations, not physical memory or consumer storage.
The consumer can retain blocks or consume each block immediately.

Preserve its error value and stop before the next block after an error.
Cancellation stops preparation and prevents a successful final fold result.
Consumer effects before cancellation remain visible. No transactional callback guarantee applies.
Preserve borrowed input and quantizer windows and aliases.

## Validation

Test raster order, odd dimensions, padding, quantizer order, exact block limits and malformed admission.
Test all cancellation checkpoints, consumer errors, retained blocks and aliases.
Compare complete integer coefficients with an independent numeric and plane reference on native and Wasm.
Connect the fold to the baseline frame writer and decode coefficients with an independent JPEG reference.
Run the complete image suites and applicable Linux checks before publication.

## Remaining scope

This internal module does not select a public quality scale, sample factors, alpha policy or color conversion.
The public encoder decisions need user input. Full JPEG coverage and measured performance remain open.
