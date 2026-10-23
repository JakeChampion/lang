# Fractal tile renderer performance (2026-10-23)

The renderer advances four pixels with two independent `f64x2` vectors.
Each lane retains its first escape count and magnitude.
Separate arithmetic operations preserve the scalar order for the color calculation.
Exact orbit comparisons stop periodic interior pixels early.
The renderer keeps the same pixel centers, double precision, iteration limits, and palette.

An initial CPU profile placed 183 of 199 samples in the scalar escape function.
The [profile](baseline.folded) comes from baseline `c6cccf62f` with the pinned stage0 compiler.
PNG compression and color calculations account for a small part of that sample.

## Measurements

The [raw results](results.json) compare baseline `09706b670` with the optimized renderer.
Both versions use one compiler, `-O`, and the `x86-64-linux` target.
The host is an AMD EPYC 9V74 with a four-CPU quota.
Each row shows the median of five complete tile renders.
Baseline and candidate runs alternate their order.

The timer includes pixel computation, color calculation, and PNG compression.
It excludes the checksum that the benchmark uses to check its output.

| Tile | Baseline ms | Optimized ms | Speedup |
| --- | ---: | ---: | ---: |
| Whole set, z0 | 13.70 | 12.71 | 1.08× |
| Seahorse, z14 | 44.13 | 33.82 | 1.30× |
| Deep dive, z20 | 287.49 | 149.48 | 1.92× |
| Deep dive, z30 | 303.93 | 157.76 | 1.93× |
| Deep dive, z38 | 573.98 | 286.36 | 2.00× |
| Deep dive, z42 | 582.73 | 274.40 | 2.12× |
| Period-three interior, z20 | 410.62 | 13.35 | 30.75× |
| Spiral, z15 | 66.91 | 40.11 | 1.67× |
| Exterior, z42 | 8.37 | 7.90 | 1.06× |

The script compares the complete PNG bytes for all nine tiles before the timed runs.
Every PNG matches the baseline.
The report records coordinates, compiler and source hashes, all runs, byte counts, and checksums.

The iteration counter stops at escape, an exact cycle, or the per-pixel limit.
Cycle detection reduces this count for interior pixels.
Deep boundary pixels can still require thousands of iterations.
Zoom alone does not determine cost: the exterior tile at z42 takes less time than the whole-set tile at z0.

These timings describe this host and workload.
The correctness and allocation suites also pass on ARM64 and WASI.

## Reproduce

Build the compiler from this change, then run:

```sh
python3 docs/benchmarks/fractal-2026-10-23/compare.py \
  --fern bin/fern --baseline 09706b670 --runs 5 --output /tmp/fractal-results.json
```

The compiler must admit `__f64x2_of`, `__f64x2_splat`, and `__f64x2_lane` in FIP functions.
The compile-path verifier and runtime allocation tests check these operations.
The script selects the native Linux or Apple Silicon target for its host.
