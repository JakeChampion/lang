# Mandelbrot reference comparison

The adapted [Roy Ward AVX2 renderer](https://github.com/royward/optimized-mandelbrot/tree/1051f96ee98ad347e153f56cc71dd4b8403e911a) shows substantial performance headroom.
This comparison measured the application before the acceleration changes.
The Fern source is from commit `8c5e704903500165dbddccf5e092ea5fbda373c8`.

Each version computes the same 256 by 256 pixel tiles on one CPU thread.
The timer includes escape computation and storage of each escape result.
It excludes color calculation, PNG compression, and output serialization.
The host is an AMD EPYC 9V74 with a four-CPU quota.
Each result is the median of five runs, with version order rotated between runs.

| Tile | Fern kernel ms | Same algorithm in C++ ms | Adapted AVX2 reference ms |
| --- | ---: | ---: | ---: |
| whole | 2.24 | 1.34 | 2.09 |
| seahorse14 | 18.11 | 8.48 | 4.06 |
| dive20 | 135.59 | 65.81 | 20.45 |
| dive30 | 141.32 | 75.43 | 21.64 |
| dive38 | 272.57 | 158.90 | 40.63 |
| dive42 | 268.89 | 139.18 | 42.84 |
| minibrot20 | 15.10 | 6.53 | 28.85 |
| spiral15 | 29.76 | 14.94 | 6.13 |
| exterior42 | 1.29 | 0.67 | 0.75 |

## Output checks

All 589,824 pixel results match across the three versions.
Each result contains an escape count and the bits of the double-precision escape magnitude.
For bounded pixels, the comparison uses a count equal to the tile iteration limit and a magnitude of -1.
This normalization permits analytic interior tests and exact cycle detection to stop early.
The unmodified palette uses no escape count for bounded pixels.

The reference keeps the same pixel centers, operation order, escape radius, and iteration limits as Fern.
The adapter replaces fused arithmetic with separate arithmetic operations.
It records the first escape magnitude from the reference replay loop.
It also replaces the reference coordinate calculation with the Fern coordinate calculation.
The original downloaded files remain in `orange/`, and the adapted files are in `adapted/`.
Their license notices remain present.

The AVX2 reference has four independent vectors with four doubles each.
It refills finished lanes with new pixels and checks escape conditions in groups of four iterations.
A scalar replay recovers the first escape result.
It has no analytic interior test or cycle detector, which explains its slower periodic-interior result.
These measurements do not use its contour-fill algorithm.

The C++ translation keeps the baseline two-vector algorithm, analytic tests, and exact cycle detector.
Its improvement suggests that compiler output deserves attention alongside changes to the renderer.
The Fern assembly contains vector spills and scalar branches in the inner loop.
This experiment does not isolate the cost of each instruction or compiler decision.

## Reproduce

`prepare.py` adapts the downloaded snapshot and creates the Fern kernel driver.
`same_kernel.py` translates the existing Fern kernel into a C++ diagnostic variant.
`compare.py` runs the three binaries and writes `results.json`.
The JSON report includes all runs, source hashes, coordinates, and the result-stream hash.
Set `FERN_BASELINE` to a native compiler built from the baseline revision.
Run these commands from this directory.

The native compiler is GCC 14.2.0.
The reference flags are `-O3 -std=c++20 -mavx2 -mfma -ffp-contract=off -DBROADWELL_OR_EARLIER=1`.
The diagnostic translation uses `-O3 -std=c++20 -msse2 -ffp-contract=off`.
Fern uses `-target x86-64-linux -O`.
All variants retain double precision and disable fused arithmetic.

```sh
python3 prepare.py
python3 same_kernel.py
g++ -O3 -std=c++20 -mavx2 -mfma -ffp-contract=off -DBROADWELL_OR_EARLIER=1 adapted/bench.cpp adapted/Mandelbrot_fastest_raw.cpp -o orange-kernel
g++ -O3 -std=c++20 -msse2 -ffp-contract=off same-kernel.cpp -o same-kernel
"$FERN_BASELINE" -target x86-64-linux -O -o fern-kernel fern/kernel.fern ../../../../internal/stdlib
./fern-kernel --raw > fern.bin
./same-kernel --raw > same.bin
./orange-kernel --raw > orange.bin
python3 compare.py --fern "$FERN_BASELINE"
```

## Further references

[Roy Ward's optimization notes](https://www.orange-kiwi.com/posts/optimization-example-mandelbrot-part-2/) explain lane reuse, escape replay, and contour methods.
Contour filling can miss small features, so these timings use the full pixel calculation.

[Fraktaler 3](https://fraktaler.mathr.co.uk/) is a useful deep-zoom reference.
Its [algorithm notes](https://mathr.co.uk/web/deep-zoom.html) explain perturbation and bivariate linear approximation.
These methods reuse a reference orbit and skip ranges of iterations under validity checks.
They need error checks, rebasing, and fallback paths, and do not guarantee the same floating-point bytes as direct iteration.
They have not been benchmarked in this experiment.

[FractalShark](https://github.com/mattsaccount364/FractalShark) is a useful CUDA reference for a GPU deployment.
It is not part of this CPU comparison.
