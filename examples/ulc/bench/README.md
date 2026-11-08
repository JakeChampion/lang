# ULC benchmarks

Measured on 2026-11-08 with an Intel Xeon Platinum 8573C and a four-CPU container quota.
Fern has lower median elapsed time in all 32 measured rows against the tuned upstream C tools.
The narrowest lead is 1.039× for long, single-worker noise decoding.
These results cover the configurations below and do not establish a universal performance ranking.

## Method

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
The sanitizer unit run reports 191613 allocations, 191613 frees, and zero live bytes.

These synthetic inputs do not replace speech and music collections, other hardware, or listening panels.
The measurements do not establish speed for other quality settings, block sizes, or rate-control modes.

## Earlier measurements

The [output-buffer candidate](x86-64-native-before-batching-one-worker-100s.json) still lost the long noise-decode row before batching and radix-4 stages.
The older [four-worker long run](x86-64-four-workers-100s.json), [four-worker short run](x86-64-four-workers-10s.json),
and [single-worker short run](x86-64-one-worker-10s.json) compare earlier code against an `-O2` SIMD C build.
Their compiler and executable hashes differ from the current records.
The [first single-core baseline](x86-64-simd.json) predates the throughput changes.
