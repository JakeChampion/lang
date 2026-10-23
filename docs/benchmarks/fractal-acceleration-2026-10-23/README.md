# Fractal acceleration, 2026-10-23

The default renderer reduces the measured zoom-42 boundary tile from 277 ms to 39 ms, about 7 times faster.
The direct `f64` renderer reduces the same tile from 280 ms to 135 ms and preserves every PNG byte.
The default renderer uses a shared approximation. It need not reproduce the old image byte for byte.

## Complete tile measurements

Each measurement renders one 256 by 256 tile on one CPU thread, including color calculation and PNG compression.
The host is an AMD EPYC 9V74 with a four-CPU container quota.
The table contains medians from seven runs, with baseline and candidate order alternating.
The baseline is commit `8c5e704903500165dbddccf5e092ea5fbda373c8`, with its compiler.
The candidate uses the updated compiler and default renderer.

| Tile | Baseline ms | Default ms | Speedup |
| --- | ---: | ---: | ---: |
| whole set | 12.13 | 11.98 | 1.01x |
| seahorse, zoom 14 | 31.77 | 24.72 | 1.29x |
| boundary, zoom 20 | 145.13 | 68.07 | 2.13x |
| boundary, zoom 30 | 153.25 | 33.38 | 4.59x |
| boundary, zoom 38 | 284.09 | 81.76 | 3.47x |
| boundary, zoom 42 | 276.59 | 39.41 | 7.02x |
| period-three interior, zoom 20 | 14.39 | 3.55 | 4.05x |
| spiral, zoom 15 | 40.82 | 29.26 | 1.40x |
| exterior, zoom 42 | 8.03 | 9.00 | 0.89x |

These samples do not establish constant render time at every zoom or location.
Cheap exterior tiles have a small regression from output allocation and scheduling overhead.
The same deep boundary can require different work when its iteration limit or reference prefix changes.
[results.json](results.json) contains coordinates, every run, image differences, and source and compiler hashes.

## Changes

The compiler uses AVX three-operand forms for packed arithmetic on x86-64.
A spilled right operand can remain in memory, which removes copies and reloads.
Floating-point comparisons branch directly on flags while preserving unordered NaN behavior.
A portable SSA pass replaces multiplication by two with addition to self.
Both lane widths have sign-bit extraction builtins on x86-64, ARM64, WASI, and both interpreters.

The direct renderer has four independent `f64x2` streams and refills completed lanes.
It advances four updates per escape test and replays a finished group to recover the first escape result.
Exact periodicity detection and analytic interior tests remain active.
The application uses 128-bit lane values. It does not yet use four-double AVX2 vectors.

At deep zoom, the renderer builds one double-double reference orbit for the tile.
A normalized degree-16 Taylor model skips a shared prefix through small reference values.
Its error estimate includes discarded polynomial products and accumulated arithmetic error.
Bivariate linear approximation composes additional aligned blocks under validity-radius checks.
A skipped interval must remain below the escape radius.
The renderer resumes direct SIMD iteration at the resulting state.

It uses direct iteration when the prefix is too short or its reconstructed state is invalid.
A model that covers the full iteration limit can return a bounded tile immediately.
The estimates are numerical guards, not a formal proof of all floating-point error.

[direct.json](direct.json) measures the lane-refill renderer without a shared prefix.
[batched.json](batched.json) measures the smaller batch renderer with the updated compiler and grouped escape tests.
Both comparisons preserve every PNG byte across all nine tiles.
They include algorithm changes, so they do not isolate compiler speedup alone.

## Numerical checks

An independent GCC `__float128` oracle computes each sampled orbit with 113-bit precision.
It uses the same input pixel coordinates, escape radius, and iteration limit.
The grid contains 289 pixels per tile, including corners and the center, for 2,601 samples.
The oracle has no analytic, cycle, SIMD, polynomial, or linear shortcuts.

| Tile | Default classification differences | Direct classification differences | Default escape-count differences | Direct escape-count differences |
| --- | ---: | ---: | ---: | ---: |
| boundary, zoom 20 | 0 | 0 | 2 | 2 |
| boundary, zoom 30 | 0 | 0 | 1 | 8 |
| boundary, zoom 38 | 0 | 2 | 4 | 55 |
| boundary, zoom 42 | 0 | 1 | 1 | 76 |
| each other tile | 0 | 0 | 0 | 0 |

Classification means bounded or escaped at the configured iteration limit.
It does not prove mathematical membership in the Mandelbrot set.
The default path has fewer deep-zoom differences from this oracle than direct `f64` iteration in these samples.
It still has escape-count differences, including one difference of 316 updates at zoom 30.
The samples cannot exclude artifacts elsewhere.
[quality.json](quality.json) includes the complete counts and maximum errors.

The default and baseline PNGs differ at 24.7% of pixels in the zoom-42 sample.
The mean absolute channel difference is 6.36 on the 0–255 scale.
Five other tiles retain identical bytes. Deep boundary differences are expected because the baseline also has numerical error.
The benchmark validates PNG chunk checksums, decompression, dimensions, and RGB row layout.
The application tests also compare lane scheduling against a scalar oracle on x86-64, ARM64, and WASI.

## Fast CPU reference

The [Roy Ward AVX2 comparison](reference/README.md) motivated lane refilling and grouped escape tests.
Its adapted kernel computes the same baseline escape results for all 589,824 pixels across nine tiles.
The measured zoom-42 kernel takes 42.84 ms, compared with 268.89 ms for the baseline Fern kernel.
Those times exclude color and PNG encoding. Compare these kernel times separately from the complete-tile table above.
The adapter retains the upstream license and uses a pinned source revision.

[Fraktaler 3](https://fraktaler.mathr.co.uk/) and its [deep-zoom notes](https://mathr.co.uk/web/deep-zoom.html) describe perturbation and linear composition.
These notes informed the shared reference and validity checks.
This report does not include a Fraktaler runtime measurement.

## Reproduce

Build native Fern compilers from the baseline and candidate checkouts.
Set `FERN_BASELINE` and `FERN_CANDIDATE` to their absolute paths.
Run from the repository root:

```sh
python3 docs/benchmarks/fractal-2026-10-23/compare.py \
  --fern "$FERN_CANDIDATE" --baseline-fern "$FERN_BASELINE" \
  --baseline 8c5e704903500165dbddccf5e092ea5fbda373c8 --runs 7 \
  --allow-pixel-differences --output /tmp/fractal-results.json
python3 docs/benchmarks/fractal-2026-10-23/compare.py \
  --fern "$FERN_CANDIDATE" --baseline-fern "$FERN_BASELINE" \
  --baseline 8c5e704903500165dbddccf5e092ea5fbda373c8 --runs 5 \
  --candidate-function render_exact --output /tmp/fractal-direct.json
python3 docs/benchmarks/fractal-acceleration-2026-10-23/check_quality.py \
  --fern "$FERN_CANDIDATE" --output /tmp/fractal-quality.json
```

The numerical check requires GCC on x86-64 Linux for `__float128`.
Use `--candidate-function render_reference` to reproduce the batch comparison.
Use `--images /tmp/fractal-images` to save the baseline and candidate PNGs for visual inspection.
