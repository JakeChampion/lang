# ULC benchmarks

Measured on 2026-11-08 with an Intel Xeon Platinum 8573C and a four-CPU container quota.
The FLOAT32/VBR suite records 131 lower Fern medians across 144 comparisons against tuned upstream C.
The geometric mean of C/Fern ratios is 1.374×, with equal weights for all cases.
Without silence, the ratio is 1.253× across 108 cases.
Several cases still favor C, particularly large-block noise decode.
These results do not establish a universal performance ranking.

## Integer PCM output

Source `a0fee5aed7260df5f6878f146e5ced7644fa0549` adds direct paired PCM conversion.
It preserves the scalar writer's rounding, clipping, and exact output bytes.
The input is PCM16 at 44.1 kHz, encoded by tuned upstream C at VBR quality 75.
Both decoders read the same C bitstream.
The C build, hardware, and CPU affinity match the FLOAT32 method below.
No other local builds or tests run during timing.

The suite has 168 decode comparisons, each with eleven alternating repetitions.
Ten-second fixtures cover mono and stereo at blocks 256, 2048, and 32768.
Each configuration uses PCM8, PCM16, and PCM24 output with one and four workers.
The remaining configurations use 100-second stereo fixtures at block 2048.

Fern has lower medians in 116 of 168 cases.
The geometric-mean C/Fern ratio is 1.455× overall and 1.081× without silence.
The fixed-suite bootstrap interval is 1.432× to 1.482×, with 2000 repetitions and seed 417.
The 52 losses remain in the raw data.
Large-block noise still favors C.
These measurements do not establish a universal ranking.

Each row below combines three block sizes and four input types.

| Channels | Output | One-worker wins | One-worker geometric mean | Four-worker wins | Four-worker geometric mean |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1 | PCM8 | 8/12 | 1.326 | 9/12 | 1.688 |
| 1 | PCM16 | 9/12 | 1.307 | 9/12 | 1.461 |
| 1 | PCM24 | 8/12 | 1.275 | 9/12 | 1.492 |
| 2 | PCM8 | 5/12 | 1.352 | 9/12 | 1.682 |
| 2 | PCM16 | 7/12 | 1.327 | 8/12 | 1.480 |
| 2 | PCM24 | 6/12 | 1.261 | 9/12 | 1.500 |

The 100-second stereo results use the default block size:

| Input | PCM8 one/four workers | PCM16 one/four workers | PCM24 one/four workers |
| --- | ---: | ---: | ---: |
| silence | 7.401 / 6.466 | 4.509 / 3.865 | 3.174 / 3.050 |
| tone | 1.001 / 1.885 | 1.007 / 1.702 | 0.971 / 1.557 |
| noise | 0.869 / 1.216 | 0.877 / 1.176 | 0.858 / 1.123 |
| transients | 1.017 / 1.748 | 1.037 / 1.822 | 1.005 / 1.410 |

[The 42 raw reports](x86-64-pcm.json) include all times, hashes, and oracle errors.
The oracle output is clipped to the PCM range before each error calculation.
The Fern error bound includes half of one quantization step.
FLOAT32 checks retain their original tolerance.

[The before-and-after probes](x86-64-pcm-before-after.json) use 31 alternating repetitions on stereo noise.
The candidate binary matches the committed build byte for byte.
Every output WAV matches the previous writer byte for byte.
The ten-second, block-32768 probes improve by 1.88× to 1.98×.
The 100-second, block-2048 probes improve by 2.29× to 2.31×.
Tuned C remains faster in these six probes.

To reproduce the PCM suite, build the pinned source in a separate worktree.
Use the commands below with source `a0fee5aed7260df5f6878f146e5ced7644fa0549`.
Add `--direction decode --output-format PCM16` and select the format for each run.
Use `--channels 1` or `--channels 2` for the short fixtures.
Combine the resulting reports with `bench/summarize.py`.

## FLOAT32 and VBR method and results

The compiler and codec source is `07136183f64ee18bab4a0b1044baac247e9e8b14`.
The suite covers all eight block sizes with one and four workers.
Each fixture lasts ten seconds, with stereo PCM16 at 44.1 kHz and VBR quality 75.
Two additional configurations use 100-second inputs and the default block size of 2048.
Each case has eleven alternating repetitions, including process startup and file I/O.
No other local builds or tests run during measurement.

The single-worker runs use CPU 0.
The four-worker runs allow CPUs 0–3 for both implementations.
Upstream C remains single-threaded.
The Fern worker count is a maximum, because short files can take a serial path.
The C source, libfourier source, and compiler flags match the earlier method below.

A win means a lower median elapsed time.
Each configuration includes four inputs and both operations, for eight comparisons.
A C/Fern ratio above 1 means Fern is faster.
The next table gives the minimum and maximum ratios across the ten-second cases.

| Block size | One-worker wins | One-worker C/Fern range | Four-worker wins | Four-worker C/Fern range |
| ---: | ---: | ---: | ---: | ---: |
| 256 | 6/8 | 0.977–2.918 | 8/8 | 1.266–2.251 |
| 512 | 8/8 | 1.040–2.439 | 7/8 | 0.878–2.479 |
| 1024 | 8/8 | 1.061–2.369 | 7/8 | 0.938–2.335 |
| 2048 | 8/8 | 1.081–2.366 | 8/8 | 1.162–2.692 |
| 4096 | 8/8 | 1.051–2.166 | 6/8 | 0.933–1.876 |
| 8192 | 8/8 | 1.080–2.340 | 8/8 | 1.087–2.110 |
| 16384 | 7/8 | 0.855–2.578 | 7/8 | 0.800–1.789 |
| 32768 | 6/8 | 0.788–2.623 | 5/8 | 0.718–2.261 |

The 100-second default-block runs give these ratios:

| Input | Operation | One worker | Four workers |
| --- | --- | ---: | ---: |
| silence | encode | 1.345 | 1.432 |
| silence | decode | 2.340 | 2.184 |
| tone | encode | 1.159 | 1.729 |
| tone | decode | 1.289 | 1.610 |
| noise | encode | 1.007 | 1.896 |
| noise | decode | 1.090 | 1.145 |
| transients | encode | 1.153 | 1.788 |
| transients | decode | 1.161 | 1.611 |

[All eighteen raw reports](x86-64-block-sweep.json) retain individual times, input hashes, executable hashes, and compiler hashes.
A bootstrap of paired repetitions gives a 95% interval of 1.344× to 1.398× for the aggregate ratio.
The calculation uses 2000 repetitions and seed 417.
This interval covers repetitions within this fixed suite.
It does not quantify other inputs, machines, or runs.

The oracle checks both decoder outputs outside the timed repetitions.
The maximum Fern error against the double-precision FFT is `2.38e-7` across these fixtures.
The records include C errors separately, without a wider Fern tolerance.

## Reproduce the FLOAT32 and VBR suite

Build the C tools with the flags in the reproduction section below.
Build the Fern tools from the measured source:

```sh
git worktree add /tmp/ulc-measured 07136183f64ee18bab4a0b1044baac247e9e8b14
make -C /tmp/ulc-measured build
make -C /tmp/ulc-measured/examples/ulc
make -C /tmp/ulc-measured/examples/ulc build/reference64
python3 examples/ulc/bench/run.py \
  --fern /tmp/ulc-measured/examples/ulc/build/ulc --compiler /tmp/ulc-measured/bin/fern \
  --oracle /tmp/ulc-measured/examples/ulc/build/reference64 \
  --source-commit 07136183f64ee18bab4a0b1044baac247e9e8b14 \
  --reference /tmp/ulc-reference/release \
  --reference-label 'GCC -O3 -march=native -fno-math-errno -funroll-loops with x86 SIMD (-D_M_X64)' \
  --seconds 10 --repetitions 11 --cpu 0 --threads 1 --block-size 256 \
  --output /tmp/ulc-report-256-one.json
```

Repeat for all block sizes and worker counts.
Use `--cpus 0,1,2,3 --threads 4` for the four-worker runs.
Add the two 100-second runs at block size 2048.
Combine the reports and reproduce the bootstrap interval:

```sh
python3 examples/ulc/bench/summarize.py \
  --output /tmp/ulc-sweep.json /tmp/ulc-report-*.json
```

## Earlier default-block method

The next four result tables record source `15212d32dc49807e204a9767467ff574a3f3fd98`.
Fern has lower median elapsed time in all 32 comparisons in those tables.
Their narrowest lead is 1.039× for long, single-worker noise decode.

- Fern compiler and codec source: `15212d32dc49807e204a9767467ff574a3f3fd98`.
- C source: `b47df8d50c2ef2d765e5dbec0bf567b5e558173a`.
- libfourier source: `bbacb86911ab8911a1285f05afcedd52b095113a`.
- C compiler: GCC 14.2.0 with `-D_M_X64 -O3 -march=native -fno-math-errno -funroll-loops`.
- Input: deterministic silence, tones, noise, and transients. Stereo PCM16 at 44.1 kHz.
- VBR quality: 75. Block size: 2048. Each input lasts 10 or 100 seconds.
- Each row has 11 repetitions. The tools alternate execution order between repetitions.
- Timing includes process startup, encoding or decoding, and file I/O. No other local builds or tests run during measurement.
- Both tools decode the same C-generated stream to FLOAT32. Maximum sample difference is 2.15e-6, below the 1e-5 limit.
- Single-worker runs use CPU 0. Four-worker runs allow CPUs 0–3 for both tools and set `ULC_THREADS=4` for Fern.

The upstream C tools remain single-threaded when four CPUs are available.
The single-worker rows compare both implementations on one CPU.
Each raw JSON record contains all individual times, input hashes, encoded sizes, executable hashes, and the compiler hash.
A C/Fern ratio above 1 means Fern is faster.

## One worker, 100-second inputs

[Raw measurements](x86-64-native-one-worker-100s.json).

| Input | Operation | C seconds | Fern seconds | C/Fern ratio |
| --- | --- | ---: | ---: | ---: |
| silence | encode | 0.5783 | 0.4354 | 1.328 |
| silence | decode | 0.0745 | 0.0330 | 2.253 |
| tone | encode | 1.4520 | 1.3701 | 1.060 |
| tone | decode | 0.1349 | 0.1056 | 1.277 |
| noise | encode | 1.5498 | 1.4578 | 1.063 |
| noise | decode | 0.1347 | 0.1296 | 1.039 |
| transients | encode | 1.4320 | 1.3424 | 1.067 |
| transients | decode | 0.1395 | 0.1089 | 1.281 |

## Four workers, 100-second inputs

[Raw measurements](x86-64-native-four-workers-100s.json).

| Input | Operation | C seconds | Fern seconds | C/Fern ratio |
| --- | --- | ---: | ---: | ---: |
| silence | encode | 0.5885 | 0.4530 | 1.299 |
| silence | decode | 0.0751 | 0.0335 | 2.244 |
| tone | encode | 1.4495 | 0.8368 | 1.732 |
| tone | decode | 0.1356 | 0.0844 | 1.606 |
| noise | encode | 1.5659 | 0.8986 | 1.743 |
| noise | decode | 0.1367 | 0.1144 | 1.195 |
| transients | encode | 1.4556 | 0.8418 | 1.729 |
| transients | decode | 0.1359 | 0.0888 | 1.531 |

## One worker, 10-second inputs

[Raw measurements](x86-64-native-one-worker-10s.json).

| Input | Operation | C seconds | Fern seconds | C/Fern ratio |
| --- | --- | ---: | ---: | ---: |
| silence | encode | 0.0576 | 0.0433 | 1.328 |
| silence | decode | 0.0088 | 0.0040 | 2.182 |
| tone | encode | 0.1438 | 0.1304 | 1.103 |
| tone | decode | 0.0169 | 0.0114 | 1.477 |
| noise | encode | 0.1499 | 0.1386 | 1.081 |
| noise | decode | 0.0157 | 0.0140 | 1.121 |
| transients | encode | 0.1872 | 0.1715 | 1.092 |
| transients | decode | 0.0186 | 0.0141 | 1.321 |

## Four workers, 10-second inputs

[Raw measurements](x86-64-native-four-workers-10s.json).

| Input | Operation | C seconds | Fern seconds | C/Fern ratio |
| --- | --- | ---: | ---: | ---: |
| silence | encode | 0.0903 | 0.0606 | 1.489 |
| silence | decode | 0.0106 | 0.0048 | 2.216 |
| tone | encode | 0.2004 | 0.1247 | 1.607 |
| tone | decode | 0.0215 | 0.0161 | 1.331 |
| noise | encode | 0.1952 | 0.1248 | 1.564 |
| noise | decode | 0.0189 | 0.0146 | 1.291 |
| transients | encode | 0.1963 | 0.1338 | 1.467 |
| transients | decode | 0.0180 | 0.0139 | 1.291 |

## Reproduce

Build a clean C reference with x86 SIMD enabled.
The upstream architecture detector needs `_M_X64` for this GCC build.

```sh
git clone --recurse-submodules https://github.com/Aikku93/ulc-codec /tmp/ulc-reference
git -C /tmp/ulc-reference checkout b47df8d50c2ef2d765e5dbec0bf567b5e558173a
git -C /tmp/ulc-reference submodule update --init --recursive
make -C /tmp/ulc-reference/libfourier clean
make -C /tmp/ulc-reference clean
make -C /tmp/ulc-reference/libfourier -j2 \
  CCFLAGS='-D_M_X64 -O3 -march=native -fno-math-errno -funroll-loops -Iinclude'
make -C /tmp/ulc-reference -j2 \
  CCFLAGS='-D_M_X64 -O3 -march=native -fno-math-errno -funroll-loops -Iinclude -Ilibfourier/include'
```

Build the Fern compiler and codec from the measured source commit.
The bootstrap uses the stage0 compiler pinned in that commit.

```sh
git checkout 15212d32dc49807e204a9767467ff574a3f3fd98
./bootstrap/bootstrap.sh build
bin/fern-selfhost -target x86-64-linux -o /tmp/fern-ulc \
  compiler/fern.fern internal/stdlib
/tmp/fern-ulc -target x86-64-linux -stdlib internal/stdlib \
  -o /tmp/ulc-fern examples/ulc/ulc.fern
python3 examples/ulc/bench/run.py \
  --fern /tmp/ulc-fern --compiler /tmp/fern-ulc \
  --source-commit 15212d32dc49807e204a9767467ff574a3f3fd98 \
  --reference /tmp/ulc-reference/release \
  --reference-label 'GCC -O3 -march=native -fno-math-errno -funroll-loops with x86 SIMD (-D_M_X64)' \
  --seconds 100 --repetitions 11 --cpu 0 --threads 1 \
  --output /tmp/ulc-benchmark.json
```

Use `--cpus 0,1,2,3 --threads 4` for the four-worker comparison.
Use `--seconds 10` for the shorter inputs.
Select allowed CPUs on Linux, or omit affinity options on systems without CPU affinity support.
The runner also accepts `--block-size`, `--channels`, `--sample-rate`, and `--quality` for other configurations.

## Implementation and validation

- Four interleaved FFTs share packed float SIMD butterflies. File decoding pairs stages in radix-4 butterflies and removes zero-twiddle rotations.
- Input rotation includes the first FFT stage. SIMD fused multiply-add reduces rotation instructions.
- File decoding retains planar float32 samples at an exact scale of 2^96. The general block API retains double-precision lapping.
- Exact GF(2) tables generate 32 noise signs and the final seed. Decaying tails preserve every scalar float rounding step.
- Constant noise runs reuse scaled magnitude bits. Quantizer scaling moves outside ordinary coefficient loops.
- Full-window reconstruction writes directly into the output planes. File decoding initializes the final managed WAV buffer once.
- The unsafe allocation primitive retains ordinary size guards and ownership. Every output byte is initialized before observation, including silent blocks and padding.
- Numeric buffers use bulk allocation. Transforms reuse FFT, overlap, coefficient, and sample buffers.
- Coefficient priorities use a stable radix sort. CBR and ABR use count-only rate searches.
- Encoder workers use detector checkpoints and exact overlap preroll. Linux decoder workers write disjoint ranges of a shared mapping.

Native, ARM64/QEMU, and WASI codec units pass.
The native integration suite covers all block sizes, WAV formats, window patterns, and overlap scales.
It also covers mono, stereo, and three-channel inputs.
Parallel output matches serial bytes in every tested format and rate mode.
Exact noise tests cover all 512 ordinary run lengths and all 256 decay codes at both quantizer limits.

A scalar generator checks the noise tables.
`python3 tests/generate_noise_tables.py --write` regenerates them from the codec directory.
The sanitizer unit run for source `15212d32` reports 191613 allocations, 191613 frees, and zero live bytes.

These synthetic inputs do not replace speech and music collections, other hardware, or listening panels.
The measurements do not establish speed for other quality settings, block sizes, or rate-control modes.

## Accuracy reference

The optional `--oracle` argument selects `tests/reference64.fern`, which uses a double-precision FFT and overlap.
The oracle runs outside the timed repetitions.
The runner records each decoder's maximum and RMS error against this reference.
It keeps the strict `1e-5` bound for Fern.
Without this option, the runner keeps its original C/Fern comparison.

At 32768 samples, tuned C differs from this reference by up to `3.11e-5` on the noise stream.
Fern differs by at most `2.38e-7` across the endpoint fixtures.
This check attributes the difference without accepting a wider Fern error bound.

Build the oracle from the measured checkout:

```sh
/tmp/fern-ulc -target x86-64-linux -stdlib internal/stdlib \
  -o /tmp/ulc-reference64 examples/ulc/tests/reference64.fern
```

Add `--oracle /tmp/ulc-reference64 --block-size 32768` to the benchmark command for the large-block comparison.

## Earlier measurements

The [output-buffer candidate](x86-64-native-before-batching-one-worker-100s.json) still lost the long noise-decode row before batching and radix-4 stages.
The older [four-worker long run](x86-64-four-workers-100s.json), [four-worker short run](x86-64-four-workers-10s.json),
and [single-worker short run](x86-64-one-worker-10s.json) compare earlier code against an `-O2` SIMD C build.
Their compiler and executable hashes differ from the current records.
The [first single-core baseline](x86-64-simd.json) predates the throughput changes.
