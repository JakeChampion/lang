# Color primitives

The color module depends only on pixels and Fern standard math.
No codec, HTTP, or external color library enters this module.
The API operates on pixel values. It does not transform complete images.

## Encoded sRGB and linear light

`srgb_to_linear` and `linear_to_srgb` accept normalized f64 values.
They use the standard piecewise sRGB transfer functions.
Both clamp inputs to [0, 1]. They map NaN to zero.
Byte conversion clamps and rounds to the nearest byte, with ties upward.
Tests check all 256 encoded bytes with a transfer round-trip tolerance of 0.000001.

`grayscale` converts encoded RGB to linear light.
It calculates relative luminance with coefficients 0.2126, 0.7152, and 0.0722.
It encodes that luminance back to sRGB and preserves alpha.
There is no ICC profile support or profile relabel operation.

## Alpha contracts

Pixel values contain straight encoded sRGB and linear coverage alpha.
`rgb` sets opaque alpha. `rgba` preserves all four channels.
`with_alpha` changes coverage without a color conversion.
`swap_rb` exchanges red and blue without an alpha change.

`Premultiplied` explicitly identifies premultiplied encoded sRGB bytes.
`premultiply` multiplies each encoded byte by alpha with integer nearest rounding.
This operation does not convert the color to linear light.

`unpremultiply` divides with integer nearest rounding and clamps inconsistent channels to 255.
Zero alpha returns zero RGB. The original transparent color cannot survive premultiplication.
Low alpha can lose substantial color precision. Round trips have no fixed one-byte error guarantee.

`source_over` takes straight pixel values and converts colors to linear light.
It composites premultiplied linear values with Porter-Duff source-over rules.
It returns straight encoded sRGB and rounded coverage alpha.
Two fully transparent inputs return transparent black.
Half-opacity white over opaque black produces sRGB 188 at byte alpha 128.
An encoded-channel blend produces a different value.

## Validation

The native test command includes four color cases.
They cover transfer thresholds, all byte round trips, grayscale, channel order, and saturated channels.
They also cover partial alpha, zero alpha, opaque alpha, and known composite values.
All four cases pass under the Fern heap sanitizer with zero live bytes.
SIMD, complete-image operations, ICC profiles, and HDR remain outside this module.
