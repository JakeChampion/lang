# JPEG component frames

**Status:** Internal prerequisite in progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The direct MCU producer emits entropy without a complete frame.
Color assembly needs component identifiers, table declarations and checked frame boundaries.

## Solution

Extend the internal frame writer with a complete single-scan component serializer.
The caller supplies explicit component identifiers, sample factors and quantization, DC and AC selectors.
Tables use consecutive destinations from zero, with all required definitions in the output.
The synchronous producer supplies complete natural-order blocks in MCU order.
Reuse the qualified geometry and entropy producer.

The new entry point emits SOI, DQT, SOF0, DHT, optional DRI, SOS, entropy and EOI.
It does not emit APP markers or assign a color interpretation.
The original single-component entry points retain their exact bytes and JFIF behavior.
The public image encoder retains its unresolved quality and alpha decisions.

## Contracts

Support one through four components in one complete baseline scan.
Identifiers are unique bytes. Factors range from one through four.
Interleaved factor products sum to at most ten blocks per MCU.
One-component scans retain one block per MCU, regardless of their factors.

Quantization destinations range from zero through three and retain transmitted order.
Huffman destinations range from zero through one for each table class.

Reject invalid dimensions, component metadata, table shapes and limits before callbacks or output allocation.
Canonical Huffman checks retain the entropy producer's cancellation contract.
Include every marker, prefix, stuffed byte and EOI byte in the exact output limit.
Close the entropy consumer before any source or codec result escapes.
Preserve source error types, first-error behavior, cancellation and caller aliases.

## Tests

Compare complete bytes with an independent marker and bit-string reference.
Recover complete physical component grids through offline libjpeg.
Use odd dimensions, nonintegral factors, partial edge blocks, whole dummy blocks and distinct table selectors.
Check unique identifiers, malformed tables, exact output limits and every cancellation checkpoint.
Check producer counts, source errors, ignored failures, escaped consumers and aliases.

Retain earlier grayscale frame bytes and all existing image tests.
Run native, Wasm and applicable Linux checks with heap evidence.

Keep legal boundary dimensions in the byte campaign if an offline decoder has a smaller limit.
Record that external limit separately from coefficient reconstruction.
Do not reduce Fern coverage or classify other decoder failures as limit results.

## Remaining scope

This internal component serializer does not select color registration, sample reduction, public image controls or metadata policy.
Other JPEG processes and complete format coverage remain active requirements.
No performance or physical memory claim applies.

## Reference

[ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), B.1.1.4, B.2.1 through B.2.4.4, defines the marker and table contracts.
