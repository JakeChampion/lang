# Benchmark status

Performance: not measured.
Image quality: not measured.
Comparison with libvips: not measured.

The test suite proves selected functional properties only.
No throughput or parity claim follows from its results.

Before M2, define a dataset with provenance and license records.
Include photos, line art, gradients, transparency, noise, and extreme dimensions.
Record hardware, CPU baseline, compiler commit, flags, warm-up policy, and concurrency.

Measure latency distributions, throughput, peak RSS, and allocations.
Compare matched output dimensions, alpha rules, coordinate maps, and image quality.
Separate decode, resize, encode, and complete request measurements.

Do not use smaller output size as a quality measure.
