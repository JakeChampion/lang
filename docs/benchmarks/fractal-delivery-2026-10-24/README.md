# Fractal delivery and codec measurements, 2026-10-24

This change reduces tile latency, stops obsolete render work, and exposes the cost of each stage.
The application retains its 128-bit SIMD renderer and adds a faster lossless PNG preset for deep views.
The compiler also supports more numeric tables without heap allocation.

## Method

The host uses an AMD EPYC 9V74 CPU with x86-64 Linux.
Each comparison uses seven runs and reports the median.
The scripts alternate or rotate variant order.
No other benchmark ran at the same time.
These are development-host measurements, not deployment or network guarantees.
The JSON files contain raw runs, source hashes, and compiler hashes.

[complete-results.json](complete-results.json) compares the complete tile renderer against main at `e20f0ae15`.
Each side uses its own compiler and standard library.
The driver measures compute, color, and PNG together, without HTTP or cooperative server yields.
Every decoded RGB pixel matches the baseline.
PNG bytes can differ because the default renderer selects fast compression at zoom 20 and above.

| Tile | Main ms | New ms | Speedup |
| --- | ---: | ---: | ---: |
| whole | 12.46 | 11.20 | 1.11x |
| seahorse14 | 26.82 | 22.89 | 1.17x |
| dive20 | 68.74 | 65.76 | 1.05x |
| dive30 | 36.65 | 27.72 | 1.32x |
| dive38 | 81.21 | 76.00 | 1.07x |
| dive42 | 39.20 | 32.36 | 1.21x |
| minibrot20 | 3.84 | 2.80 | 1.38x |
| spiral15 | 30.54 | 28.61 | 1.07x |
| exterior42 | 9.37 | 7.41 | 1.26x |

Tile cost still depends on the orbit and prefix quality.
The adjacent zoom-38 case remains slower than zoom 42.
The renderer does not promise constant cost at every zoom.

## Delivery and metrics

The executable digest identifies the renderer in every tile URL.
An executable change invalidates browser and CDN tile keys without a cache purge.
If the executable cannot be read, both caches are disabled.

The browser limits its own cache to 16 MiB and 256 PNG tiles.
It gives the viewport center priority and shares duplicate requests.
HTTP permits two active requests, and WebSocket permits one.

The browser aborts obsolete HTTP requests and sends an explicit cancellation token for both transports.
The server checks that token between rows and before PNG compression.
About every four milliseconds, it polls the event loop for one millisecond.
A zero-duration poll did not let the cancel request reach an active render.

The live WebSocket check acknowledged a deep-tile cancellation in 7.4 ms on this host.
The HTTP regression test confirms that an active render stops without an increase to completed tiles.
These checks do not set a fixed cancellation deadline.

The cancellation registry belongs to one container.
A deployment with multiple containers needs request affinity for the control route.

The UI replaces lifetime averages with a recent median and p95 at the current zoom.
It measures latency through image decode and exposes compute, color, PNG, queue, and transfer estimates.
Local and CDN cache hits have separate counters.
Cache responses do not contribute historical server times to render-stage statistics.
Browser resource timings identify HTTP cache reuse even when response headers retain an old cache status.

## PNG and DEFLATE

`std/png` adds all five PNG filters and an adaptive row selector.
It adds balanced and fast compression options.
The existing encoder defaults remain unfiltered and balanced, with identical output bytes.
Unfiltered rows use the shared byte-copy kernel.
`std/deflate.deflate_fast` uses one hash candidate per position, with no predecessor chain or lazy match search.
It retains the full 32 KiB match window.

[benchmark_png.py](benchmark_png.py) encodes identical RGB buffers through Fern and libpng 1.6.43.
The libpng reference links the system zlib library.
The Fern baseline uses the original PNG module with the same compiler as the new module.
The test isolates encoder time from orbit and color work.
An independent Python decoder checks checksums, filters, and all RGB bytes for every mode.
The results are in [png-results.json](png-results.json).

| Tile and encoder | Median ms | PNG bytes |
| --- | ---: | ---: |
| whole, baseline | 6.077 | 23,765 |
| whole, fern-none-balanced | 5.379 | 23,765 |
| whole, fern-none-fast | 3.546 | 49,209 |
| whole, libpng-none-fast | 0.947 | 34,725 |
| dive20, baseline | 7.673 | 65,167 |
| dive20, fern-none-balanced | 7.081 | 65,167 |
| dive20, fern-none-fast | 4.494 | 67,836 |
| dive20, libpng-none-fast | 2.084 | 66,425 |
| dive42, baseline | 10.941 | 71,729 |
| dive42, fern-none-balanced | 9.868 | 71,729 |
| dive42, fern-none-fast | 4.628 | 79,320 |
| dive42, libpng-none-fast | 1.828 | 77,647 |
| minibrot20, baseline | 2.203 | 270 |
| minibrot20, fern-none-balanced | 1.785 | 270 |
| minibrot20, fern-none-fast | 1.358 | 270 |
| minibrot20, libpng-none-fast | 0.228 | 939 |
| exterior42, baseline | 2.750 | 725 |
| exterior42, fern-none-balanced | 2.228 | 725 |
| exterior42, fern-none-fast | 1.361 | 725 |
| exterior42, libpng-none-fast | 0.246 | 1,322 |

The zoom-42 fast preset reduces encoder time from 10.941 ms to 4.628 ms.
Its file grows from 71,729 to 79,320 bytes, an increase of 10.6%.
The whole-set fast preset more than doubles file size, so shallow tiles retain balanced compression.
Sub and adaptive filters cost more on these tiles and do not justify a new application default.
Libpng remains faster, so the shared compressor and block encoder still have measurable scope for improvement.
The application does not add a native libpng dependency.

## Numeric tables

The palette again uses ordinary `f64[]` tables with indexed interpolation.
Literal `f64` tables already used static storage.
The missing cases were `f32` arrays and constant floating expressions.
The compiler now handles literal values, negation, float casts, and arithmetic with constant float operands.
It preserves intermediate `f32` rounding, signed zero, and subnormal values.
It keeps computed NaN operations at runtime because target payloads can differ.

A shared budget permits at most 32 SSA node visits per candidate.
It prevents exponential work when expressions reuse earlier values.
Unsupported or over-budget expressions retain the runtime path.

The tests check zero allocation from the first read on x86-64, ARM64, and WASI.
They also check full-width integer values and copy-on-write updates that preserve shared originals.
This is a compiler optimization, with no new table API or palette-specific special case.

## Numerical regression budget

[check_numerics.py](check_numerics.py) compares 12,015 points across 15 tiles.
Each tile contains a 17-by-17 grid, 256 seeded random points, and 256 edge points.
Cases include the cardioid cusp, a period-two tangent, a critical orbit, and adjacent deep tiles.
The oracle uses 113-bit `__float128` arithmetic at the same input coordinates and iteration limits.
The test also shifts the reference center one tile east and increases the reference radius.

The accelerated renderer disagrees with the oracle on bounded/escaped classification for 5 points.
Direct `f64` disagrees for 9 points.
Every case has a p95 escaped-count error of zero, but a rare count error reaches 529 updates.
All sampled tile edges agree on classification between the two reference centers.
These checks do not prove exact boundary pixels or complete seam freedom.
The renderer remains an approximation near chaotic boundaries.

CI permits at most 0.5% classification differences per case and a p95 count error of one update.
The same limits apply to agreement between reference centers.
The check exits with a failure if either budget is exceeded.
[numerics.json](numerics.json) contains the per-case errors and example coordinates.

## SIMD width investigation

[benchmark_simd.py](benchmark_simd.py) compares four C++ implementations of the same direct lane-refill algorithm.
Each retains analytic interior tests, exact cycles, four-update escape groups, and scalar replay.
The variants use 128-bit or 256-bit vectors with two or four independent streams.
Automatic vectorization is disabled to keep the requested widths separate.
The script also measures the current Fern direct kernel.
All 589,824 escape counts and magnitude bit patterns agree across all five implementations.

Bounded lanes normalize their counts to the iteration limit before comparison.

The measurement includes the direct kernel and result storage only.
It excludes shared-prefix preparation, color, PNG, serialization, and transport.
The complete default renderer often skips much of this direct work.
Results appear in [simd-results.json](simd-results.json).

| Tile | C++ 128x2 ms | C++ 128x4 ms | C++ 256x2 ms | C++ 256x4 ms | Fern 128x4 ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| whole | 1.445 | 1.526 | 1.414 | 1.468 | 2.648 |
| seahorse14 | 8.059 | 6.792 | 6.251 | 5.580 | 10.509 |
| dive20 | 53.148 | 38.819 | 32.964 | 25.651 | 60.028 |
| dive42 | 106.162 | 79.311 | 69.433 | 49.716 | 121.010 |
| minibrot20 | 5.882 | 4.735 | 4.045 | 2.936 | 7.110 |
| exterior42 | 0.869 | 0.826 | 0.793 | 0.880 | 1.851 |

For zoom 42, four AVX2 streams reduce direct-kernel time from 79.311 ms to 49.716 ms against four narrow streams.
The width gain is 1.60x within the same compiler and algorithm.
The cheap exterior case does not benefit from four wide streams.
The C++ and Fern gap also includes compiler code generation and unused prefix machinery.
It cannot establish the speedup of a new Fern vector type by itself.

The script records static instruction counts and vector stack moves for each C++ variant.
The four-stream wide variant contains 568 vector stack moves, against 305 in the four-stream narrow variant.
These are static counts, not dynamic spill measurements.
They show why register pressure and spill code need review alongside vector width.
The application still uses `f64x2` on every backend.
This change supplies the requested wider-vector investigation without a new public SIMD type.

## Reproduction

The scripts require Python 3 and GCC with AVX2 and `__float128` support.
The PNG reference also requires libpng and zlib headers and libraries.
A development compiler without an embedded standard library uses the checkout modules directly.
An embedded compiler must contain the updated modules.

```sh
bin/fern -target x86-64-linux -O -o /tmp/fern-dev compiler/fern.fern internal/stdlib
python3 docs/benchmarks/fractal-delivery-2026-10-24/check_numerics.py \
  --fern /tmp/fern-dev --check --output /tmp/numerics.json
python3 docs/benchmarks/fractal-delivery-2026-10-24/benchmark_png.py \
  --fern /tmp/fern-dev --baseline e20f0ae15 --runs 7 --output /tmp/png.json
python3 docs/benchmarks/fractal-delivery-2026-10-24/benchmark_simd.py \
  --fern /tmp/fern-dev --runs 7 --output /tmp/simd.json
python3 docs/benchmarks/fractal-2026-10-23/compare.py \
  --fern /tmp/fern-dev --baseline-fern /path/to/main-fern \
  --baseline e20f0ae15 --allow-pixel-differences --runs 7 --output /tmp/complete.json
```

`--png-include` and `--png-library` select alternate libpng headers and linker arguments.
The browser tests use Node and require no package installation.
The Go tests use independent PNG and DEFLATE decoders on x86-64, ARM64, and WASI.
They also run the numerical budget and live HTTP cancellation check.
A manual Chromium check covers navigation, transport changes, cancellation, local cache reuse, and mobile layout.
