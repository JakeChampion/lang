# JPEG component blocks

**Status:** Internal prerequisite in progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The forward transform requires complete blocks, but image components can have odd dimensions and row padding.

## Intended behavior

Extract one owned eight-bit block from a borrowed component plane.
The caller supplies dimensions, stride, block coordinates and edge behavior.
Support edge replication and constant fill without a default policy.
Keep samples in row order for the forward transform.

## Implementation decisions

- Validate layout and minimal storage with wide arithmetic before sample access.
- Permit storage to end after the final image sample without final row padding.
- Reject block origins outside the plane. Fill only the partial final block.
- Do not interpret row padding as image samples.
- Check cancellation before allocation, during rows and before success.
- Preserve input windows and aliases. Keep all runtime code in Fern.

## Validation

Test odd and tiny dimensions, strides, all block edges, explicit fill values and large invalid extents.
Test bounds, cancellation and borrowed windows.
Compare exact blocks with an independent Python oracle on native and Wasm.
Run applicable Linux and full image suites before publication.

## Remaining scope

This helper does not select color conversion, sample factors, quality or a public image encoder contract.
Full JPEG coverage and measured performance remain active requirements.
