# JPEG MCU order

**Status:** Internal prerequisite in progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The current writer treats each block as a complete MCU for one component.
A color writer needs component-specific block positions and complete MCU boundaries.
Partial image edges can require entire dummy blocks.

## Solution

Add an internal geometry inspector and a block-address fold for a complete frame in one scan.
Accept one through four components in frame order, with explicit horizontal and vertical sample factors.
Each factor ranges from one through four.
For interleaved scans, the sum of factor products cannot exceed ten.
A single-component scan always contains one block per MCU, regardless of its factors.

The inspector returns MCU dimensions and exact block counts without an image-sized allocation.
The fold visits MCUs in raster order, then components in frame order.
Each component contributes its horizontal-by-vertical block rectangle in raster order.
Addresses retain actual component sample dimensions and identify entire dummy blocks.
The fold does not read sample planes or select an edge-fill policy.

## Contracts

Frame dimensions range from one through 65535 for this known-height DCT prerequisite.
Component dimensions use the ceiling of the frame dimension times its factor divided by the maximum factor.
Do not require integer ratios between sample factors.
Calculate total block admission in wide integers before work or callbacks.
The caller supplies an exact maximum block count.

Validate dimensions, component count, sample factors and block limits before cancellation callbacks.
Check cancellation before traversal, before each block callback and before success.
Propagate consumer errors immediately without later callbacks.
Prior consumer effects remain after an error.
Borrow the component list and keep all traversal state local to the call.

## Validation

Test literal MCU orders, odd dimensions, nonintegral ratios and all legal factor combinations.
Include one-component factors larger than one and the ten-block boundary.
Use independent integer geometry and offline libjpeg header observations.
Test malformed dimensions, factors, component counts, exact limits, every cancellation checkpoint and consumer failures.
Run native and Wasm checks with their applicable heap evidence.

## Remaining work

The color entropy writer must maintain a DC predictor for each component.
Restart intervals count complete MCUs and reset every component predictor.
Table selectors, frame serialization, sample reduction and the public image API remain separate stages.
The unresolved JPEG option and alpha questions remain unchanged.
This stage does not complete JPEG support or establish a performance claim.

## Reference

[ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), A.1.1, A.2.1 through A.2.4, B.2.2 and B.2.3.
