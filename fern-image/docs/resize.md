# Scalar resize

The resize module depends on pixels and color.
It preserves RGB8 or RGBA8 format and returns a tight row stride.
`resize` takes exact positive output dimensions, a filter, and destination resource limits.
`fit_dimensions` returns dimensions inside an aspect-ratio box.
It permits upscales and rounds the other dimension to the nearest integer, with a minimum of one.

## Coordinate contract

The source map for destination coordinate x is `(x + 0.5) * source_width / output_width - 0.5`.
The y map follows the same rule.
Nearest-neighbor selects `floor((x + 0.5) * source_width / output_width)` and clamps to the source edge.
Bilinear clamps the continuous map at zero and replicates source edge pixels.
It interpolates four samples with f64 weights.

Bilinear converts encoded sRGB to linear light and premultiplies by coverage before interpolation.
It divides by interpolated coverage and encodes the result back to straight sRGB.
Zero coverage produces transparent black.
Byte conversion rounds to nearest with ties upward.
Nearest-neighbor preserves all bytes, also for transparent pixels.

## Safety and limits

The source descriptor must pass the pixel access validator.
The destination constructor checks dimensions, overflow, pixel limits, and byte limits before allocation.
Both source and destination remain complete images.
There is no additional frame-sized scratch array.
Immutable updates can copy destination storage. Peak process memory remains unbounded by a single image limit.

These filters do not provide area integration or an antialias filter for large downscales.
Strong downscales can alias even when the scalar result matches the coordinate contract.
Cancellation, no-upscale, cover, crop, rotation, and SIMD remain future work.

## Verification

Seven native cases check solid colors, single rows, single columns, odd dimensions, and extreme aspect ratios.
Golden outputs check nearest selection, two-dimensional RGB interpolation, and transparent color isolation.
Rejection cases cover invalid dimensions, multiplication overflow, caller budgets, and malformed source storage.
All seven cases pass with the heap sanitizer and zero live bytes.

A synthetic benchmark creates a 32x32 RGBA gradient and performs ten 16x16 bilinear resizes.
Its five process samples include startup and source construction.
See `benchmarks/results/resize-m2.json` for raw measurements and compiler identification.
No libvips comparison or real-image quality result follows from this small baseline.
