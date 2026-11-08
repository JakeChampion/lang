# JPEG MCU entropy output

**Status:** Internal prerequisite in progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The existing entropy writer maintains one DC predictor and counts restart intervals in blocks.
Color scans need a separate predictor for each component and complete-MCU restart intervals.
The writer must retain direct block production without a complete coefficient plane.

## Solution

Extend the internal entropy writer with a separate complete-scan producer entry point.
Accept one through four components in frame order, with explicit horizontal and vertical factors.
Each component selects explicit DC and AC table destinations.
Baseline scans permit one or two destinations for each table class.
The existing single-component entry points retain their exact behavior and bytes.

The producer supplies complete natural-order coefficient blocks through a synchronous consumer.
Each MCU follows the existing geometry module's component and block order.
One-component scans always contain one block per MCU, regardless of their factors.
Interleaved components contribute their factor products, with at most ten blocks per MCU.
The caller supplies the exact number of MCUs, restart interval and output byte limit.

## State and errors

Retain one DC predictor for each component.
Before the first MCU after a restart boundary, align the entropy bytes and emit the next restart marker.
Reset every predictor at that boundary. Do not emit a restart marker after the final MCU.
Reuse the existing canonical Huffman, coefficient checks, bit output, byte stuffing and alignment machinery.

Admit structural shapes, table selectors and counter limits before callbacks or allocation.
Validate canonical tables under the existing cancellation contract.
Copy only bounded component metadata and table codes into private call state.
Remove output state from its cell before user callbacks or append operations.
Retain first-error poisoning, immediate consumer errors and closed-consumer rejection.
Close and clear the private state before any result escapes.

## Validation

Compare exact bytes with an independent bit-string reference and offline JPEG coefficient reconstruction.
Use distinct component DC sequences, table selectors, sample factors and restart intervals.
Cover stuffed bytes, marker cycling, exact byte limits and complete dummy blocks.
Test malformed shapes, selectors, tables, coefficients, producer counts, cancellation and caller aliases.

Test ignored callback failures, source errors and escaped consumers.
Retain the existing busy-state and closed-state rejection guards.
Run native and Wasm execution with applicable heap evidence.
Retain all earlier single-component reference bytes.

## Remaining scope

Frame serialization, color sample reduction and the public image encoder remain separate stages.
Public JPEG option and alpha questions remain unresolved.
This internal entry point does not complete JPEG or establish a performance or physical memory claim.

## Reference

[ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), A.2, B.2.3, B.2.4.4 and F.1.2, defines baseline scan order and entropy output.
