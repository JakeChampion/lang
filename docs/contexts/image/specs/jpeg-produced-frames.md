# JPEG frames from block producers

**Status:** Internal prerequisite in progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The component fold emits one block at a time, but the frame writer requires all coefficients in one array.
The writer needs an internal producer interface before the component pipeline can avoid that array.

## Intended behavior

A producer supplies borrowed complete blocks to a private entropy consumer.
The caller declares the exact count. The writer retains bit state, DC prediction and output limits.
Reject missing or extra blocks. Preserve the first codec error even when the producer ignores callback errors.
Preserve producer errors when no codec error occurs.

Add a frame adapter and an internal component-plane writer.
Require explicit quantizers, Huffman tables, edge policy, restart interval and limits.
Keep the array entry points compatible. All runtime code remains Fern.

## Contracts

Validate counts, table shapes and limits before producer callbacks.
Validate each block length before coefficient access.
The producer must call its consumer synchronously during its invocation.
The consumer closes when the producer returns. Later calls return a shape error.

Failed calls do not yield a partial output. Earlier producer effects remain visible.

Restart markers separate blocks. Do not emit a marker after the final block.
Retain exact output limits, stuffing, final alignment, cancellation and caller ownership.
Keep encoder state private to each call. A failed callback poisons the call.
Do not treat a block count or encoded-byte limit as a physical memory bound.

## Validation

Compare complete bytes with the array writer and an independent entropy reference.
Recover complete frame coefficients with offline libjpeg.
Test malformed blocks, counts, tables, limits, cancellation and retained aliases.
Test producer errors and ignored consumer failures. Test consumer use after return.
Run native, Wasm, Linux and full image suites before publication.

## Remaining scope

No public quality, color, alpha or sample-factor default applies.
The public image decisions remain open. This checkpoint does not complete JPEG support.
Full format coverage and comparable performance remain active requirements.
