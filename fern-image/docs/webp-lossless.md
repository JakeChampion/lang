# Lossless WebP literal pixels

`vp8l.decode_simple` reads a complete VP8L chunk payload, starting at its
0x2f signature and ending before any RIFF padding byte. It returns a
tightly packed RGBA8 image. The caller supplies pixel and byte limits and
a cancellation callback. The decoder checks the layout and the complete
literal bit extent before allocating output. It checks cancellation on entry
and before every row.

For a complete file, use `webp.decode_simple` with an additional input byte limit.
It validates the RIFF and WEBP identifiers, file length, single VP8L chunk extent,
and zero padding for an odd-sized chunk before passing the payload to the decoder.

| Feature | Current coverage |
|---|---|
| Lossless pixel data | One prefix-code group with simple one- or two-symbol trees and literal green, red, blue and alpha samples. Each channel can select its value independently. A single-symbol tree consumes zero pixel bits. |
| Alpha | The alpha tree yields straight 8-bit alpha, including partially transparent samples. RGB values under zero alpha are preserved. The header's alpha hint has no effect on decoding. |
| Subtract-green transform | Reconstructs red and blue by adding green modulo 256, including for fully transparent pixels. |
| Color-indexing transform | One to 256 delta-coded RGBA entries, including packed 1-, 2- and 4-bit indices. Palette and image data must both use simple literal codes. |
| Other VP8L tools | Combined transforms, predictor and cross-color transforms, color caches, multiple groups, general Huffman trees and LZ77 copies are outside this entry point. |
| Container | Single-chunk RIFF/WEBP files containing a supported VP8L payload. Extended WebP, metadata chunks and animation require separate admission and validation. |
| Lossy WebP | VP8 frames and separate ALPH streams require their own codecs. |
| Encoding | `writer.encode_simple` emits a single VP8L chunk for RGB8 or RGBA8 images with at most two distinct values per channel. A third value in any channel returns `Unsupported`. |

The writer validates the source layout, skips row padding and preserves RGB samples under transparent alpha. It measures the exact encoded length before allocating the file, including the RIFF pad byte. Both passes check for cancellation at row boundaries.

The checked-in tests compare exact RGBA bytes from libwebp for a two-row
mixed-color image and a constant image. They also exercise every truncated
prefix, trailing data, a transform flag, cancellation and the output byte
limit. The mixed fixture declares its green values in descending order so
that reading a bit as a symbol-list index would swap the green samples.

[WebP lossless bitstream specification](https://developers.google.com/speed/webp/docs/webp_lossless_bitstream_specification)
