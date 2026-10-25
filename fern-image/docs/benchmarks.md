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

## M4 sparse row observations

The row benchmark represents a ten-million-pixel solid source and nearest output graph.
It requests five sparse rows, each with 2,000 output bytes.
The logical scratch limit is 12,000 bytes.
The implementation does not allocate a complete source or destination pixel array.

Three process samples took 2.803, 2.066, and 1.960 milliseconds.
Their Linux child RSS observations were 6,844 KiB each.
The RSS counter includes the Python collector inherited at fork before exec.
It is not an isolated measurement of Fern RSS or a hard memory limit.

Raw results record the compiler, binary, source hashes, host, and collection limits.
Wall time includes startup, graph validation, row execution, and process cleanup.
The shared host has no isolation. These samples do not establish throughput or libvips parity.

Use a compiler with the native append fix from PR #13208.
Run these commands from this package directory to collect another record:

```sh
../bin/fern -target x86-64-linux -o build/row-benchmark benchmarks/row_pipeline.fern
python3 benchmarks/measure_rows.py build/row-benchmark ../bin/fern /tmp/row-result.json
```

See `benchmarks/results/row-pipeline-m4.json` for the original observations.
Recoverable OOM, allocator overhead, caller retention, and aggregate service memory remain unqualified.
