# Lossless WebP literal pixels

`vp8l.decode_simple` reads a complete VP8L chunk payload, starting at its
0x2f signature and ending before any RIFF padding byte. It returns a
tightly packed RGBA8 image. The caller supplies pixel and byte limits and
a cancellation callback. The decoder checks the layout and the complete
literal bit extent before allocating output. It checks cancellation on entry
and before every row.

| Feature | Current coverage |
|---|---|
| Lossless pixel data | One prefix-code group with simple one- or two-symbol trees and literal green, red, blue and alpha samples. Each channel can select its value independently. A single-symbol tree consumes zero pixel bits. |
| Alpha | The alpha tree yields straight 8-bit alpha, including partially transparent samples. RGB values under zero alpha are preserved. The header's alpha hint has no effect on decoding. |
| Other VP8L tools | Transforms, color caches, multiple groups, general Huffman trees and LZ77 copies are outside this entry point. |
| Container | Callers supply the raw payload. RIFF demux, extended WebP, ICC, EXIF, XMP and animated frames require separate admission and validation. |
| Lossy WebP | VP8 frames and separate ALPH streams require their own codecs. |
| Encoding | No WebP writer is provided. |

The checked-in tests compare exact RGBA bytes from libwebp for a two-row
mixed-color image and a constant image. They also exercise every truncated
prefix, trailing data, a transform flag, cancellation and the output byte
limit. The mixed fixture declares its green values in descending order so
that reading a bit as a symbol-list index would swap the green samples.

[WebP lossless bitstream specification](https://developers.google.com/speed/webp/docs/webp_lossless_bitstream_specification)
