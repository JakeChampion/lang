# 0003. Define the first ICC destination transform

**#13659 decision, November 2026.** The first managed color transform has one destination: 8-bit encoded sRGB.
The transform uses media-relative colorimetric intent with no additional adaptation and no black-point compensation.
It clips linear destination channels and does not use a gamut map.
Alpha, chroma reconstruction, and orientation keep their current byte contracts.

This record defines the policy before implementation and before a transform campaign.
It does not implement a transform or change an existing decode entry point.

## Pipeline

The transform composes kernels that have finite qualification.
Each stage keeps its current admission, errors, and cancellation polls.

| Step | Operation | Existing contract |
|---|---|---|
| 1 | Entropy decode, IDCT, and raw component planes | [planes](../jpeg-planes.md) |
| 2 | Centered chroma reconstruction with the explicit filter | [chroma](../jpeg-chroma.md) |
| 3 | JFIF full-range YCbCr to device RGB8 | [ycbcr](../jpeg-ycbcr.md) |
| 4 | Device byte `v` to normalized input `v / 255` | Exact binary64 division |
| 5 | Source curves and matrix to relative D50 XYZ | [matrix/TRC](../icc-matrix-trc.md) |
| 6 | Destination matrix solve to linear sRGB | [matrix solve](../icc-matrix-solve.md) |
| 7 | Clip each linear channel to `[0, 1]` | This decision |
| 8 | Encode with the IEC 61966-2-1 sRGB function | `color.linear_to_srgb` |
| 9 | Quantize to a byte | This decision |
| 10 | Exif orientation, if requested | [orientation](../jpeg-orientation.md) |

Step 3 keeps its RGB8 output.
The transform reads device bytes, not an intermediate with more precision.
LittleCMS `TYPE_RGB_8` input uses the same boundary, so the reference can compare the same values.

Orientation copies bytes and does not change any value.
It therefore comes after the color transform.
Resize and other operations that change pixel values come after step 9.
They operate on destination sRGB bytes under their current contracts.

## Destination

The destination matrix is the colorant data of the frozen 588-byte sRGB profile.
Its SHA-256 is `3bccbfedf06978c3035b255167afcf462a50b27805096cee038ff4dd69c650a6`.
The nine raw `s15Fixed16` numerators in row order are these values:

| Row | Red | Green | Blue |
|---|---:|---:|---:|
| X | 28576 | 25239 | 9375 |
| Y | 14581 | 46983 | 3972 |
| Z | 912 | 6361 | 46787 |

The profile creator applied the Bradford adaptation from D65 to D50 to these columns.
The `chad` tag records that adaptation.
The values are inside the admitted domain of `matrix_solve`, and the exact determinant is nonzero.
The engine stores these numerators as constants and does not read the destination from a file at run time.

The destination curve is the IEC 61966-2-1 encoding function, not the inverse of the frozen `para` curve.
The frozen profile has an upward join gap of about `8.49e-7` in linear light.
That gap is outside the qualified quadratic inverse domain ([inverse selection](../icc-inverse.md)).
`color.linear_to_srgb` is already the sRGB definition for the rest of the engine.
One sRGB definition is better than two definitions that are almost equal.

A 60-digit check on 20,000 linear values compared the IEC encoding with the frozen curve inverse.
The largest difference was `7.24e-6` encoded, which is 0.0018 code values.
The rounded byte was different at 16 values, all of them next to a rounding boundary.
This observation is a reason for the LittleCMS criterion below, not a precision bound.

## Intent, adaptation, and black point

The caller must request `Relative` intent explicitly.
The profile header preference does not select the intent.
The v4.4 matrix columns already describe media-relative D50 XYZ.
The transform therefore applies no `chad`, no scale for media white, and no adaptation in the PCS.
The source `chad` tag stays opaque, and the transform does not apply it a second time.

Black-point compensation is disabled.
A sampled source curve can map device zero to a value above zero.
The transform keeps that black point, and the clip in step 7 is the only change to it.
Black-point compensation, perceptual, saturation, and ICC-absolute intents each need a separate decision.
A request for one of these intents returns `Intent` before callbacks.

## Clipping and range errors

The clip occurs in linear destination light after step 6, on each channel independently.
A negative value or negative zero becomes positive zero.
A value above one becomes one.
There is no gamut map that keeps hue, and there is no out-of-gamut report.
ICC.1:2022 Annex F.3 puts the clip at the same position, before the inverse curves.

The transform does not clip XYZ.
A source XYZ value outside the `matrix_solve` input domain of `[-2, 2]` returns an explicit error.
That error does not change the value to fit the domain.
Nonfinite values cannot occur, because each admitted stage rejects nonfinite input and results.

Step 9 computes `floor(e * 255 + 0.5)` for the encoded value `e`.
Ties go upward.
This is the same rule as the color module and the JFIF sample converter.

## Alpha

The transform changes only the three color channels.
An RGBA8 source keeps each alpha byte without change, at the same pixel position.
The color channels are straight, not premultiplied, so the transform reads them directly.
The transform does not use the `Premultiplied` type, and the caller must not pass premultiplied bytes.
A transparent pixel gets the same color transform as an opaque pixel.

JPEG has no alpha channel.
This rule applies when a later format uses the same transform.

## Admission

The managed path admits only the profiles that `matrix_model.read` accepts with a `Relative` request.
These are v4.4 RGB input or display profiles with XYZ PCS and matrix/TRC data.
The matrix/TRC data must be the only forward model.
Each other profile returns an explicit error.
The engine never removes a profile and then decodes its samples as sRGB.

A one-component JPEG with a gray profile returns an unsupported error.
An Adobe APP14 segment, CMYK, and a LUT-based profile stay unsupported.
A JPEG with no ICC segment keeps its current behavior.
The current behavior gives the decoded device RGB to the caller without a transform.

The output image has no ICC profile.
A later encoder that writes metadata must write sRGB or no profile.

## Prepared tables

The device input has 256 values on each channel.
A later image plan can therefore compute step 4 and the source curves once.
The plan then keeps 768 linear values.
Each value must be equal, bit for bit, to the result of the scalar composition.
A table is an optimization of that composition, not a different numerical path.
Its storage needs its own logical admission.

## Criteria for the transform campaign

These criteria are fixed before the implementation.
They apply to the finite corpus of that campaign only.

1. Each output byte must equal the byte of an independent exact reference.
   A difference of one is permitted only when the exact encoded value is within `1e-9 / 255` of a boundary between two bytes.
   The campaign records each such case.

   The reference uses Python `Fraction` arithmetic for the curves, matrices, and clip.
   It uses 90-digit `Decimal` arithmetic for the powers.
2. A second check uses pinned offline LittleCMS 2.19 with the frozen profile as the destination.
   It uses `TYPE_RGB_8` input and output, `Relative` intent, black-point compensation off, and flags 324.
   Each output byte must be within one code value of the LittleCMS byte.
   The campaign records each difference.
3. Identity, gray-axis, primary, and white controls must give exact bytes on all three targets.
4. No alpha byte can change.
5. Native, Wasm, and ARM64/QEMU10 sample records must be equal.
   Each worker must report a nonzero balanced heap with zero live bytes.

A failure does not change a criterion, a domain, or a tolerance.

## Order of work

1. A scalar destination kernel: XYZ to clipped, encoded sRGB bytes, with the criteria above.
2. A scalar source-to-destination composition from `ForwardData` to sRGB bytes.
3. A row and image API over RGB8 and RGBA8, with logical admission and cancellation.
4. A managed JPEG entry point that assembles APP2, reads the model, and then transforms.
   The current strict entry points keep their behavior.

Each step needs its own pull request and finite campaign.
This decision does not complete any of them.
General ICC compatibility, photographs, performance, physical/process bounds, and production acceptance need separate evidence.
No runtime CMM dependency is part of this decision.

## References

- [ICC.1:2022](https://www.color.org/specifications/ICC.1-2022-05.pdf): section 6.3 (PCS and D50), Annex E (adaptation), and Annex F.3 (matrix/TRC and clipping).
- [IEC 61966-2-1:1999](https://webstore.iec.ch/publication/6169): the sRGB encoding function.
- [LittleCMS 2.19 public header](https://github.com/mm2/Little-CMS/blob/lcms2.19/include/lcms2.h): formats, intents, and flags.
- [Native ICC feasibility decision](../icc-feasibility.md): the staged plan that this record continues.
