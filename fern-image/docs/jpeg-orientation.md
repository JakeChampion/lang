# Explicit Exif orientation

`packages/codecs/jpeg/orientation.fern` applies an explicit `exif.Orientation` to an already decoded RGB8 or RGBA8 image.
The implementation uses Fern without an external runtime codec dependency.
Every output channel copies one input channel exactly.
This operation does not interpolate, convert colors, or change alpha arithmetic.
Transparent pixels retain their hidden RGB values.

The operation does not parse JPEG markers or select an encoded color profile.
The standalone Exif reader and strict JFIF/RGB entry points retain their behavior.
An absent Exif orientation remains `None` until the caller selects a policy.
This API requires an explicit orientation and does not substitute a default.

## API and mappings

`orientation.inspect(source, value, limits)` returns `Result[OrientPlan, OrientError]` without a cancellation callback or output buffer.
`orientation.apply(source, value, limits, cancelled)` returns a fresh owned packed `pixels.Image`.
The input retains its format and channel bytes.
Output rows contain no padding, and input padding does not enter the output.
Even `TopLeft` produces a separate packed result.

The table gives the source coordinate for output coordinate `(x, y)`.
`W` and `H` are the input width and height.

| Exif value | Orientation | Output dimensions | Source coordinate |
| --- | --- | --- | --- |
| 1 | `TopLeft` | W × H | (x, y) |
| 2 | `TopRight` | W × H | (W - 1 - x, y) |
| 3 | `BottomRight` | W × H | (W - 1 - x, H - 1 - y) |
| 4 | `BottomLeft` | W × H | (x, H - 1 - y) |
| 5 | `LeftTop` | H × W | (y, x) |
| 6 | `RightTop` | H × W | (y, H - 1 - x) |
| 7 | `RightBottom` | H × W | (W - 1 - y, H - 1 - x) |
| 8 | `LeftBottom` | H × W | (W - 1 - y, x) |

The reference is CIPA Exif 3.1, section 4.6.5.1.6, orientation tag 274.
The official PDF SHA-256 is `9cc36399a46ab7aa4a65473bb8a4945d3b044da739b8f16a01b0be11886dcff9`.
These mappings do not determine centered or co-sited component positions or an encoded color interpretation.

## Admission, ownership, and cancellation

Public image and layout records can contain invalid fields.
The planner checks dimensions, stride, arithmetic, declared bytes, and exact data length before pixel access.
Output dimensions swap for values 5 through 8.
Output pixel and byte allowances use `OrientLimits.output`, a `pixels.Limits` value.
Defaults use the existing pixel limit and 64 MiB byte limit.

`OrientPlan` contains the output layout and logical input, output, and combined byte counts.
Input bytes include source padding.
The combined count equals input storage length plus packed output length.
It must fit the positive caller `max_planned_bytes` and the absolute 64 MiB logical ceiling.
These checks precede output allocation and every cancellation callback.

The byte sum excludes capacity, metadata, compiler temporaries, allocator overhead, and extra caller retention.
It does not establish physical/process memory bounds.
The operation eagerly retains the input and output.
It uses copy-on-write semantics to preserve the input if a callback changes an external alias.
Returned output updates also preserve retained aliases and later independent calls.

The operation polls before allocation, after allocation, before each group of at most 64 pixels in each row, and before success.
The successful poll count is `3 + output_height * ceil(output_width / 64)`.
A cancellation result releases private output and returns no partial image.
Cancellation is cooperative and provides no hard deadline.

`OrientError.Image` contains the existing pixel-layout error.
`OrientError.Limit` identifies an invalid or exceeded combined byte allowance.
`OrientError.Cancelled` identifies an observed cancellation request.
Checks stop at the first error.

## Finite qualification

The independent generator constructs raw RGB/RGBA images and invokes offline Pillow 12.3 `ImageOps.exif_transpose`.
It compares exact dimensions and every channel byte, without a tolerance.
The corpus contains 832 cases: 528 successful outputs and 304 expected errors.

Pillow independently observes 704 structurally valid raw inputs, including cases that Fern rejects by limits or cancellation.
The other cases check invalid public records against explicit policy expectations.
Pillow is a test reference only.

The corpus includes all eight orientations, single axes, odd dimensions, padded rows, random inputs, transparent pixels, and 64-pixel group boundaries.
Invalid cases include overflow, stride/storage mismatches, exact admission boundaries, and cancellation before allocation through final success.
Five TAP groups also check inverse mappings, copy-on-write isolation, fresh calls, and every cancellation poll on both dimension classes.
Eight collector fault controls check pixels, metadata, errors, partial outputs, process exits, heap census, case identity, and reference shape.

Run `scripts/test-jpeg-orientation.sh native`, `wasm`, or `arm64` with an absolute `FERN` path.
Set `PYTHON` to the pinned Pillow reference environment.
Each fresh worker must report one nonzero balanced heap census with zero live blocks.
Failures retain inputs, output, completed prefixes, and a failed record, and exit nonzero.
Final source-pinned campaign records and full regression evidence remain pending for this source commit.

These finite observations do not establish encoded JPEG compatibility, codec parity, color management, universal memory/time bounds, or production acceptance.
M5 and epic acceptance remain open.
