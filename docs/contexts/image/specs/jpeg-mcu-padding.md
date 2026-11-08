# Complete JPEG MCU sample blocks

**Status:** Internal prerequisite in progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The component extractor rejects block origins outside the actual sample plane.
Interleaved MCUs can require entire dummy blocks beyond that plane.
The color writer needs explicit source samples for those blocks.

## Solution

Extend the internal component extractor with a separate padded-block entry point.
Keep the original block entry point and its bounds behavior unchanged.
The new entry point accepts an explicit extent in block columns and rows.
That extent must contain the complete actual component block grid.
It does not allocate a padded image or change image dimensions.

Replicate repeats the final actual row or column, including entire dummy blocks.
Constant fill retains actual samples and fills every outside sample with the explicit byte.
Neither policy reads source row padding as samples.
The caller selects the same explicit edge policy as the existing component extractor.
This prerequisite does not select a public JPEG edge policy.

## Admission and lifetime

Validate actual layout and minimal storage before the padded extent and requested block bounds.
Report invalid extents as layout errors and outside block coordinates as bounds errors.
Finish admission before cancellation callbacks or allocation.
Retain wide source-footprint arithmetic and prevent overflow in dummy block coordinates.
The caller can describe a large extent without an extent-sized allocation.

Each successful call owns one complete row-major block and preserves all input aliases.
Check cancellation before allocation, before each row and before success.
Keep original source samples, byte padding and source dimensions unchanged.
No partial result escapes on failure.

## Validation

Compare complete blocks with an independent coordinate reference.
Cover partial edges, entirely outside rows and columns, constant fills and replication.
Include all MCU addresses from representative sample factors and odd dimensions.
Check minimal storage, input windows, output aliases, malformed extents, large coordinates and every cancellation checkpoint.
Retain original extractor and component-frame reference checks.
Run native, Wasm and applicable Linux execution with heap evidence.

## Remaining scope

Color entropy still needs separate DC predictors, complete-MCU restarts, table selection and frame serialization.
Public JPEG option and alpha decisions remain unresolved.
Complete format coverage, the wider shared model and native performance remain active work.
This prerequisite does not establish throughput or physical memory bounds.

## Reference

[ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), A.2.4, defines completion of partial MCUs and recommends edge replication.
