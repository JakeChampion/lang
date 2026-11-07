# Baseline JPEG frame writer

The internal pure Fern writer serializes one-component baseline files from explicit quantized blocks and tables.
It does not choose pixel conversion, image quality controls or public encoder options.

`frame_writer.encode_blocks` accepts natural-order coefficients, transmitted-order quantizers, DC/AC tables and frame options.
The options select width, height, a restart interval and the maximum encoded byte count.
Dimensions must fit the JPEG frame fields. The coefficient array must cover every padded block.
Each nonzero eight-bit quantizer and each Huffman table retains caller ownership.

The output contains SOI, JFIF, DQT, SOF0, DHT, optional DRI, SOS, entropy bytes and EOI.
The JFIF declaration uses equal pixel density without physical density units.
The entropy writer checks canonical tables, coefficient ranges, required symbols and restart cycles.
The complete byte limit includes marker segments, entropy escapes and EOI.
Cancellation returns no partial public output.

`Dimensions`, `Shape` and `Table` reject invalid frame or quantizer inputs.
`Limit` covers restart range and encoded bytes. `Cancelled` preserves cancellation.
`Entropy` retains other errors from the coefficient writer.

Tests check odd dimensions, complete coefficients, marker bytes, limits, cancellation and aliases.
An independent Python writer checks every emitted byte.
Offline libjpeg recovers the complete quantized coefficient sequence from each reference case.
These references remain outside the Fern runtime.

Multi-component frames, image assembly and other JPEG processes remain active requirements.
Finite tests do not establish complete JPEG support, speed or physical memory bounds.
