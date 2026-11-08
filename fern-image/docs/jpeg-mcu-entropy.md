# JPEG MCU entropy producer

The internal `entropy_writer.encode_mcus_produced` entry point emits a complete baseline scan from directly produced coefficient blocks.
All runtime code remains pure Fern.
The output contains entropy bytes and optional restart markers, without frame headers or EOI.
This helper does not select a public image quality, color or alpha policy.

`ScanComponent` contains horizontal and vertical factors and explicit DC/AC table selectors.
`ScanTables` supplies one or two destinations for each table class.
The caller lists one through four components in frame order.
Each factor ranges from one through four.
Interleaved factor products sum to at most ten blocks per MCU.
A one-component scan always contains one block per MCU, regardless of its factors.

`ScanOptions` contains the exact MCU count, restart interval and maximum output bytes.
The synchronous producer supplies exactly the required number of complete natural-order blocks.
Blocks follow component order within each MCU, with component rectangles in raster order.
The producer's source parameter retains its supplied type, with support for borrowed views.
It can read component samples and prepare one block at a time without a complete coefficient plane.

The writer retains a separate DC predictor for every component.
Restart intervals count complete MCUs.
Before the next MCU, the writer aligns with one-bits, emits the next cyclic restart marker and resets every predictor.
It emits no restart marker after the final MCU.
The existing coefficient checks, canonical Huffman codes, byte stuffing and exact output limits also apply.

Structural shapes, table selectors and counter limits fail before callbacks or allocation.
Canonical table validation retains the existing cancellation behavior.
The first codec failure poisons later consumer calls, even if the producer ignores it.
Codec errors take priority over a later source error.

The writer closes and clears private output state before any result escapes.
An escaped consumer rejects later calls. The callback protocol is synchronous.

The original single-component APIs retain their behavior and exact bytes.
Tests cover distinct predictors, table selectors, restart cycles, stuffed bytes, exact limits, malformed input and every cancellation checkpoint.
They also cover producer counts, ignored failures, source errors, aliases and escaped consumers.
An independent bit-string reference checks complete output bytes.
An offline libjpeg reader checks complete natural-order coefficients after a test-only frame wrapper.
Native and Wasm workers must report positive, balanced heaps with zero live bytes.

The image writer still needs frame serialization, sample reduction and public options.
Complete format coverage and comparable native performance remain open.
No speed or physical memory bound claim applies.
