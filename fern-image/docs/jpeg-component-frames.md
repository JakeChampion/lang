# JPEG component frames

The internal `frame_writer.encode_components_produced` entry point writes a complete baseline frame from directly produced MCU blocks.
All runtime code remains pure Fern.
Components carry explicit identifiers, horizontal and vertical factors, and quantization, DC and AC selectors.
The caller supplies complete natural-order coefficient blocks in MCU order.

The writer accepts one through four components in a complete single scan.
Component identifiers are unique bytes.
Quantization tables use consecutive destinations from zero through three.
Each table contains 64 nonzero bytes in transmitted zig-zag order.
DC and AC Huffman tables each use one or two consecutive destinations.
The existing geometry and entropy contracts determine complete MCUs, predictors and restarts.

The new entry point emits SOI, DQT, SOF0, DHT, optional DRI, SOS, entropy and EOI.
It does not emit APP markers or assign a color interpretation.
The original single-component functions retain their exact bytes and JFIF behavior.
Public color, sample reduction, image quality and alpha decisions remain separate.

Structural admission precedes callbacks and output allocation.
Canonical Huffman checks retain their existing cancellation behavior.
The output limit includes all marker segments, stuffed bytes, restart markers and EOI.
The source error retains its supplied type.
The entropy consumer closes before any result escapes.

The producer can follow the geometry fold and prepare blocks one at a time.
It must include whole dummy blocks for the complete interleaved MCUs.
Independent tests compare complete bytes and recover physical component grids through offline libjpeg.
They include odd dimensions, nonintegral factors, distinct tables, restarts, malformed input and exact limits.
Native and Wasm checks also cover cancellation, error precedence, consumer lifetime and caller aliases.

The independent byte reference covers the full admitted dimension range.
The collector records an external decoder's smaller dimension limit separately from a coefficient result.
It retains those boundary inputs and checks their complete bytes without a smaller Fern limit.
Other oracle failures remain errors.

This prerequisite does not complete the JPEG image encoder or other format modes.
No speed or physical memory bound claim applies.

[ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), Annexes A and B, defines the component and marker contracts.
