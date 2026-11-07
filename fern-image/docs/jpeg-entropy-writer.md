# Baseline JPEG entropy writer

The internal pure Fern writer accepts complete quantized blocks for one component.
It emits canonical Huffman codes, signed amplitudes, zero runs and stuffed entropy bytes.
This stage does not provide the public JPEG image writer.

Supply natural-order integer coefficients, explicit DC and AC tables and an output limit.
Each table contains sixteen code counts and its transmitted symbol sequence.
The writer rejects duplicate symbols, illegal categories, all-ones codes and oversubscribed trees.
A valid table can omit unused symbols. A required missing symbol returns `MissingSymbol`.

DC coefficients use signed eleven-bit precision. AC magnitudes must not exceed 1023.
These baseline limits are narrower than the internal quantizer's coefficient domain.
The output limit counts every byte, with stuffed zeros and restart markers.
No partial output escapes on an error or cancellation.

The optional restart interval counts blocks of the single component.
The writer resets DC prediction and cycles RST0 through RST7 between intervals.
It pads incomplete bytes with one-bits, also at the end of the scan.
It does not emit frame headers, DRI, SOS or EOI.
Multi-component MCU assembly remains a separate stage.

Unit tests cover reference bytes, coefficient boundaries, aliases, limits and every cancellation checkpoint.
An independent Python implementation checks exact entropy bytes.
An offline libjpeg oracle recovers every coefficient from complete test streams.
Both references remain test tools, outside the Fern runtime.

The full JPEG scope includes other modes, precision, color, metadata and containers.
This prerequisite does not establish complete JPEG support or a performance result.

Reference: [ITU-T T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), Annex C and F.1.1 through F.1.2.
