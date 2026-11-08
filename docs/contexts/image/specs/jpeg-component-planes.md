# JPEG component-plane assembly

**Status:** Internal prerequisite in progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The component-frame serializer accepts quantized blocks but cannot read sample planes directly.
Image assembly needs complete MCU order, edge extension, transform and quantization in one bounded protocol.

## Solution

Connect the existing component geometry, sample extractor, forward transform and quantizer to the component-frame producer.
Accept explicit eight-bit planes, layouts, edge policies, component metadata, tables and limits.
Require each plane's dimensions to match its declared sample factors and frame dimensions.
Retain padded strides and complete dummy blocks at partial frame edges.

Read source planes through a borrowed view.
Retain at most four plane descriptors during the synchronous producer call.
The descriptors share owned sample arrays under the existing value semantics.
Do not retain a complete coefficient plane or a padded sample plane.
Preserve caller aliases and earlier single-plane writer bytes.

## Admission and errors

Reject invalid component counts, geometry, plane dimensions, storage and block limits before callbacks.
The component-frame writer retains its metadata, table, restart and output-limit checks.
Transform one complete block at a time in the established MCU order.
Use the selected transmitted-order quantization table for each component.
Propagate block, transform, quantization and frame errors with explicit types.
Normalize cooperative cancellation from each stage to one cancellation result.

Retain the frame producer's first-error and closed-consumer contracts.

## Tests

Compare complete output with independent component coefficient reconstruction and exact marker and entropy references.
Test one through four components, distinct tables, nonintegral sample factors, padded strides and complete dummy blocks.
Include constant, directional, impulse and noise planes.
Retain the earlier mathematical transform and integer quantization qualification rules.

For nonuniform blocks, retain the earlier absolute DCT bound of `0.000001` before quantization.
Require each integer coefficient to lie in the interval from that bound and record ambiguous boundaries.
Require exact DC quantization and zero AC values for constant blocks.

Test malformed layouts, exact block and output limits, aliases and every cancellation checkpoint.
Run native, Wasm and applicable Linux checks with heap evidence.

## Remaining scope

This connector accepts already prepared component planes.
It does not select color interpretation, chroma reduction, public quality controls, alpha or metadata policy.
The pending public API questions remain open.
Full format coverage, comparable native performance and physical memory qualification remain separate requirements.
