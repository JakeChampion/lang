# Centered JPEG component rows

Issue #13626 is a native child of JPEG milestone #13216.
This Fern kernel reconstructs an explicit raw component row at the frame grid.
It provides nearest and bilinear filters without color conversion or transfer functions.
It does not infer encoded color meaning, apply profiles, or assemble a complete color JPEG image.

## Placement and filters

The placement reference is [JFIF 1.02, page 4](https://www.w3.org/Graphics/JPEG/jfif3.pdf).
The PDF SHA256 is `1135325aa12ee6b3def5a374fecdf3d6d0de88dc4f205af7de8d25a9c949e173`.
This API uses a uniform nominal sampling-factor grid before frame-edge cropping:

```text
u = (x + 0.5) * horizontal / max_horizontal - 0.5
v = (y + 0.5) * vertical / max_vertical - 0.5
```

Component dimensions use the ceiling of frame dimensions multiplied by the corresponding factor ratio.
Odd frame dimensions crop that nominal grid rather than change its spacing.
The sampler does not stretch a rounded component width or height to the visible frame dimensions.
This distinction matters at odd edges and fractional factor ratios.
The earlier generic image resize API uses visible size ratios and sRGB transfer functions, so it does not implement this raw-component contract.

`Nearest` selects the nearest component index, with exact half ties toward the larger index.
`Bilinear` uses the four surrounding samples and extends the edge sample beyond the component boundary.
It computes one exact two-dimensional weighted average, then rounds to the nearest byte with upward half ties.
It does not round an intermediate horizontal or vertical result.
Both filters operate directly on component bytes, without sRGB conversion, alpha composition, or range rescaling.

The implementation uses signed 64-bit integer coordinates and weights.
The declared one-through-four factor domain gives a combined bilinear denominator of at most 64.
All intermediates fit the selected integer type over the admitted dimensions and byte range.
The filters are explicit mathematical policies, not a claim that every JPEG decoder uses the same phase, coefficients, or rounding.
Neither policy establishes perceptual quality or codec parity.

## API and validation

Import `packages/codecs/jpeg/chroma`.
Construct `FrameGrid { width, height, max_horizontal, max_vertical }` from the nominal frame geometry.
Call `reconstruct_row(source, grid, row, filter, max_output_bytes, cancelled)` with a `planes.SamplePlane`.
The result owns exactly `grid.width` sample bytes.
Component identifiers remain opaque and do not establish Y/Cb/Cr or RGB meaning.

The frame dimensions must each be positive and at most 65,535, the JPEG frame-field domain.
Maximum factors must each be one through four.
The source factors must each be positive and no larger than the corresponding maximum.
The source dimensions must equal the checked ceilings implied by the frame and factor ratios.
Stride can include padding but must cover the complete component row.
The declared byte length and actual owned array length must both equal stride times height.

The source storage has a 64 MiB hard logical cap.
The requested frame row must be in bounds.
The caller's positive output-byte limit must cover the complete row, including an exact-length limit.
Validation checks all used layout fields before sample indexing or output allocation.
Invalid inputs do not invoke the cancellation callback.
Errors are `Grid`, `Sampling`, `Storage`, `Bounds`, `Limit`, and `Cancelled`.

The complete source plane remains with the caller.
Only one output row is allocated, and the API does not allocate a full upsampled component image.
Input arrays remain unchanged, while callback updates retain ordinary copy-on-write behavior.
Output updates and independent calls retain independent owned rows.
Allocator failure remains a runtime concern rather than a recoverable sampler error.

Cancellation checks occur before and after allocation, at groups of at most 64 samples, and before success.
Cancellation releases partial output.
The bulk allocation and zero fill do not contain internal polls.
Callback effects and costs remain unmetered.
Logical lengths exclude capacity, metadata, compiler temporaries, and retained caller state.
They do not establish physical/process memory bounds, aggregate admission, peak live memory, hard deadlines, or production worker qualification.

## Finite independent reference

The generator evaluates component coordinates and interpolation with Python `Fraction` arithmetic.
It uses general rational floor and rounding, with independently clamped neighbor indices.
The Fern kernel instead uses integer phase numerators and bounded denominators.
The finite criterion is exact output-byte equality without tolerance.

The bundle contains 2,030 rows and 148,650 output samples.
It covers every admitted factor ratio from one through four, both filters, odd frame dimensions, padded strides, and first/middle/last rows.
Patterns include ramps, checkerboards, impulses, deterministic random samples, and constant endpoints.
Additional controls cover exact half ties, one final two-dimensional rounding, 257-sample rows, and the 65,535 dimension boundary.
The generator records its source, Python, rational-library, and random-library identities.

The worker constructs checked public plane records and calls the row API.
The collector compares every output byte and checks row identity, width, storage length, output hashes, and the heap census.
A fresh process handles at most eight rows and must report nonzero balanced heap counts and zero final live bytes.
Each campaign locks compiler, source, reference, and tool hashes and verifies them again after cleanup.
Failures retain completed prefixes and failed input/output evidence and still return failure.
The 20-second worker timeout is an operational test limit, not a sampler deadline guarantee.

Six TAP groups cover independent filter values, identity and padding, fractional ratios, odd-frame phase, caller-constructed invalid layouts, exact admission, ownership, and every cancellation poll.
Protocol controls reject one-value differences, wrong sample order, malformed output, missing rows, incorrect metadata, and invalid heap censuses.
These finite mathematical inputs do not qualify encoded JPEG color, profiles, photographs, broad compatibility, unbounded fuzzing, security, throughput, or production operation.

## Reproduction

Run these commands from `fern-image` with a normal repository compiler build:

```sh
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-chroma.sh native
FERN="$PWD/../bin/fern" ./scripts/test-jpeg-chroma.sh wasm
QEMU_AARCH64=/path/to/qemu-aarch64 FERN="$PWD/../bin/fern" ./scripts/test-jpeg-chroma.sh arm64
python3 -m unittest discover -s tests -p qualify_jpeg_chroma_test.py
python3 tests/generate_jpeg_chroma.py tests/fixtures/jpeg-chroma-reference.json.gz
```

The reference uses only offline Python standard-library tools.
The Fern runtime has no C, Python, or external codec dependency.
New raw records use `jpeg-chroma-*` names and preserve all earlier datasets.
M5 and the epic remain open.
