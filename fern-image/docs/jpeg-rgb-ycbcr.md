# Explicit RGB to JFIF sample conversion

This internal Fern kernel converts explicit eight-bit RGB samples to full-range YCbCr samples.
It prepares samples for later component assembly.
It does not encode a complete color JPEG image.

## Numeric contract

The reference is [JFIF 1.02, page 3](https://www.w3.org/Graphics/JPEG/jfif3.pdf).
Its PDF SHA256 is `1135325aa12ee6b3def5a374fecdf3d6d0de88dc4f205af7de8d25a9c949e173`.
The implementation uses these printed decimal equations:

```text
Y  =  0.299 R  + 0.587 G  + 0.114 B
Cb = -0.1687 R - 0.3313 G + 0.5 B + 128
Cr =  0.5 R    - 0.4187 G - 0.0813 B + 128
```

All input channels use the full byte range.
The kernel evaluates the equations exactly in signed 64-bit ten-thousandths.
It rounds to the nearest integer with exact half ties toward positive infinity, then clips to the byte range.
Clipping before integer division preserves the same endpoint results.
Every intermediate fits the selected integer type throughout the byte domain.
The rounding rule is this kernel's explicit contract, not a claim that JFIF requires that rule.

The kernel applies no video-range offset, profile transform or gamma correction.
It does not identify the color interpretation of an encoded file.
Other implementations can use different coefficient precision and rounding rules.
This contract does not require exact agreement with every JPEG library.
The forward and inverse kernels do not promise a lossless round trip.

## API and ownership

Import `packages/codecs/jpeg/rgb_ycbcr`.
`convert(r, g, b)` returns `Samples { y, cb, cr }`.
`convert_row(rgb, max_output_bytes, cancelled)` accepts a borrowed packed RGB byte view.
It returns an owned byte array in packed Y, Cb, Cr order at the same resolution.
The input must contain a positive whole number of three-byte samples.
The output has exactly the same byte count as the input.

Invalid shapes return `Shape` before callbacks or allocation.
Nonpositive or exceeded byte admission returns `Limit` before callbacks or allocation.
The output also uses the existing sample-conversion cap of 64 MiB.
The kernel preserves borrowed input and copy-on-write output aliases.
Independent calls return independent owned arrays.

Cancellation checks occur before and after allocation, within groups of at most 64 samples, and before success.
Cancellation returns `Cancelled` without a partial public result.
The allocation and zero fill contain no internal cancellation checks.
The callback remains trusted code with unmetered effects.
Logical byte admission does not establish allocator capacity, process memory or a hard CPU deadline.

## Independent validation

The offline generator evaluates the printed coefficients with Python `Fraction` arithmetic.
It rounds and clips exact rational values independently of the Fern integer implementation.
The finite corpus includes all red/green pairs at blue 128, endpoint grids, neutral samples and deterministic random samples.
It contains 69,464 distinct triples in 136 rows.
Each full target campaign uses 17 fresh worker processes.

The worker compares scalar and row output before it emits complete bytes.
The collector requires exact byte agreement and a positive balanced heap census with zero live bytes.
It preserves failed inputs, outputs and the completed prefix after a failure.

Five TAP groups cover scalar values, half ties, packed order, admission, aliases and all cancellation polls.
Protocol controls reject incorrect values, channel order, lengths, missing results, wrong row metadata and invalid heap censuses.
These checks do not qualify encoded color JPEGs, chroma filters, profiles, perceptual quality, speed or physical memory bounds.

Run the reference script from `fern-image` with a qualified compiler and Python:

```sh
FERN=/path/to/fern PYTHON=/path/to/python bash scripts/test-jpeg-rgb-ycbcr.sh native
FERN=/path/to/fern PYTHON=/path/to/python bash scripts/test-jpeg-rgb-ycbcr.sh wasm
uv run --no-project python -m unittest discover -s tests -p qualify_jpeg_rgb_ycbcr_test.py
uv run --no-project python tests/generate_jpeg_rgb_ycbcr.py tests/fixtures/jpeg-rgb-ycbcr-reference.json.gz
```

Independent Python remains an offline reference. Runtime image code remains pure Fern.
