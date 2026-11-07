# QOI images

The QOI codec reads and writes RGB8 and RGBA8 images in pure Fern.
It implements the [QOI 1.0 wire format](https://qoiformat.org/qoi-specification.pdf).

## Interface

`inspect(input, limits)` checks the header and returns a packed pixel layout and color-space declaration.
It does not validate the pixel stream.

`decode(input, limits, cancelled)` validates the complete stream and returns owned image storage and its color-space declaration.
The input is a borrowed byte view.
The decoder preserves row order and channel bytes.
It does not apply a color transform or premultiply alpha.
The header selects RGB8 or RGBA8 output.

Both declarations use the same opcode rules.
The same alpha state applies to a stream with RGB output.

`encode(image, color_space, max_bytes, cancelled)` validates public image storage and returns owned encoded bytes.
It omits row padding and preserves visible channel values.
The caller supplies `Srgb` or `Linear` explicitly.
The encoder uses run, index, difference, luma, RGB and RGBA chunks.
It emits runs of at most 62 pixels and one exact end marker.

## Admission and failure

`Limits` combines pixel limits with a positive encoded-input byte limit.
Defaults reuse the pixel module's limits and the PNG input limit of 16 MiB.
Unsigned wire dimensions undergo range and checked layout validation before conversion and allocation.
The encoder accepts source storage within the current default pixel limits.
Its output byte limit includes the header, all chunks and the end marker.

The decoder rejects invalid headers, short tokens, runs beyond the image, extra chunks and invalid termination.
Every successful decode consumes exactly the declared pixels and the complete input.
Errors return no partial public image or encoded buffer.
Cancellation polls occur before admission, during pixel work and before success.
Calls have independent index and previous-pixel state.

These limits cover named logical storage and encoded bytes.
They do not establish an aggregate process memory limit or a hard CPU deadline.

## Validation

The native and Wasm image scripts include the QOI tests.
They check every opcode, initial state, wraparound arithmetic, run boundaries,
both declarations, row padding, caller aliases, truncation, limits and cancellation.

The separate reference collector uses an offline copy of the
[reference codec](https://github.com/phoboslab/qoi/blob/ffb2d2cb74a1de60819b21b939f7209aa53e91c1/qoi.h).
It checks that exact header's SHA-256 before use.
The C code is a test reference and is not a runtime dependency.

The collector decodes reference-encoded images with Fern.
It then decodes Fern output with the reference codec.
Both directions require exact pixels, dimensions, channel count and color-space declarations.
The finite corpus has 48 cases with RGB/RGBA, both declarations, runs, ramps and deterministic byte patterns.
Each worker requires balanced nonzero heap counts and zero live bytes.

Run the collector from the repository root:

```sh
uv run --no-project python fern-image/tests/qualify_qoi.py \
  --fern "$PWD/bin/fern" --reference-header /absolute/path/to/qoi.h \
  --target x86-64-linux --output /absolute/path/to/fresh-results
```

Use `--target wasm32-wasi --runner wasmtime --runner run` for Wasm.
The output directory must not exist.
The collector records source and tool hashes with the observations.
Finite checks do not establish broad compatibility or production certification.
