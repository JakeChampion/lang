# Benchmark status

A small M2 synthetic process baseline appears below.

Real-image kernel performance remains unmeasured.
Image quality and libvips comparisons remain unmeasured.

The test suite proves selected functional properties only.
No throughput or parity claim follows from its results.

Before M2, define a dataset with provenance and license records.
Include photos, line art, gradients, transparency, noise, and extreme dimensions.
Record hardware, CPU baseline, compiler commit, flags, warm-up policy, and concurrency.

Measure latency distributions, throughput, peak RSS, and allocations.
Compare matched output dimensions, alpha rules, coordinate maps, and image quality.
Separate decode, resize, encode, and complete request measurements.

Do not use smaller output size as a quality measure.

## M2 synthetic baseline

A small scalar baseline now exists in `benchmarks/resize.fern`.
Compile it from this package directory:

```sh
../bin/fern -target x86-64-linux -o build/resize-benchmark benchmarks/resize.fern
./build/resize-benchmark
```

Five process samples took 5.149, 3.087, 22.434, 3.197, and 3.072 milliseconds.
Each sample includes source construction and ten small resizes.
The shared host produced substantial timing variance.
Raw records identify the CPU, compiler hash, flags, and source hash.
These numbers do not isolate kernel latency or establish a regression threshold.
RSS, throughput, image quality, and libvips comparisons remain unmeasured.
