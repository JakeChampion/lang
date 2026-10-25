# Pixel formats

Storage is row-major and interleaved.
Pixel (0, 0) occupies the top left corner.
Its center has continuous coordinates (0.5, 0.5).

The x coordinate increases to the right. The y coordinate increases downward.
Zero dimensions are invalid.

| Format | Byte order | Channels | Alpha |
| --- | --- | --- | --- |
| RGB8 | R, G, B | 3 | Implicit 255 |
| RGBA8 | R, G, B, A | 4 | Straight, independent of color |

Each channel is an unsigned byte from 0 to 255.
Colors represent encoded sRGB values. M0 performs no transfer function or profile conversion.
Alpha represents linear coverage. It is not an sRGB color channel.

RGBA storage uses straight alpha. No premultiplication occurs.
A zero-filled RGBA image contains transparent black.
A zero-filled RGB image contains opaque black.

Stride counts bytes between row starts.
It can exceed width times channel count.
Padding remains zero after pixel writes.
`read_pixel` returns four channels, with alpha 255 for RGB8.
`write_pixel` rejects nonopaque alpha for RGB8 with `Alpha`.

RGBA8 accepts all alpha values and preserves RGB values at alpha zero.
Pixel access performs no compositing or implicit color conversion.
