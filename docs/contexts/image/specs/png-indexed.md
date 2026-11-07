# Indexed PNG input

**Status:** In progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Intended behavior

Decode noninterlaced indexed PNG samples at depths one, two, four and eight.
Expand PLTE entries to RGB8, or RGBA8 when indexed transparency is present.
Missing alpha entries are opaque. Preserve RGB samples under zero alpha.
Use the current eager, sequential, incremental and graph interfaces.

## Modules and contracts

The private palette module validates PLTE and indexed tRNS payloads.
It extracts high-bit-first indices and expands complete bounded rows.
The PNG parser enforces required chunks, multiplicity, order and output admission.
Row validation rejects all bad indices before consumer callbacks.
The incremental validator retains bounded encoded rows for filter dependencies.

Logical plans charge palettes, validation state and extra work explicitly.
Retain all previous nonindexed charges and default limits.

This stage adds no public pixel type, runtime dependency or color transform.
The encoder retains its current RGB8/RGBA8 contract.

## Validation

Test all depths, filters, palette sizes and alpha boundaries with offline Pillow.
Test every index value, unused padding bits, aliases and cancellation checkpoints.
Reject malformed chunks and out-of-range indices before row callbacks.
Verify exact logical admission and native/Wasm heaps.

Full PNG decode and encode remain required beyond this checkpoint.
This stage does not establish a speed result or a physical memory bound.

Reference: [PNG third edition](https://www.w3.org/TR/2025/REC-png-3-20250624/), sections 7.2, 11.2.2 and 11.3.1.1.
