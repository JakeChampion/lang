# ULC performance measurements

These measurements compare the complete Fern CLI with the reference C CLI.
They include process startup, file I/O, codec work, and WAV conversion.
They do not establish a worldwide speed or perceptual-quality ranking.

## Recorded runs

- Machine: INTEL(R) XEON(R) PLATINUM 8573C, with a four-core CPU quota.
- Platform: `Linux-6.18.57-x86_64-with-glibc2.41`.
- Date: 2026-11-08.
- Input: stereo PCM16 at 44.1 kHz.
- Encoder: VBR quality 75, block size 2048.
- Decoder: the same C-generated ULC2 file for both executables, with FLOAT32 output.
- Method: eleven repetitions, with alternate process order and no concurrent local builds or tests.
- Statistic: median wall-clock seconds.
- Fern compiler and codec source: `221a463ebea1329872dce679729ef70ac1f63810`.
- Reference: ulc-codec `b47df8d50c2ef2d765e5dbec0bf567b5e558173a` and its pinned libfourier.
- Reference build: GCC `-O2`, with `ARCHFLAGS=-D_M_X64` to enable x86 SIMD.

Both executables receive the same CPU affinity for each run.
The four-worker runs allow CPUs 0 through 3 and set `ULC_THREADS=4` for Fern.
The one-worker run allows only CPU 0 and sets `ULC_THREADS=1`.
The reference C CLI uses one thread in both configurations.

The upstream libfourier architecture check omits `__x86_64__` for GCC.
The explicit macro enables its existing x86 dispatch and SIMD implementations.
This comparison uses that faster build.

### Four workers, 100-second inputs

[Raw measurements](x86-64-four-workers-100s.json) include all individual times and hashes.

| Input | Operation | C seconds | Fern seconds | C/Fern speed ratio |
| --- | --- | ---: | ---: | ---: |
| silence | encode | 0.7042 | 0.5235 | 1.345 |
| silence | decode | 0.0986 | 0.0347 | 2.844 |
| tone | encode | 1.6179 | 1.0003 | 1.617 |
| tone | decode | 0.1764 | 0.1505 | 1.172 |
| noise | encode | 1.8400 | 1.0850 | 1.696 |
| noise | decode | 0.1696 | 0.1509 | 1.124 |
| transients | encode | 1.7363 | 1.0525 | 1.650 |
| transients | decode | 0.2337 | 0.1935 | 1.208 |

### Four workers, 10-second inputs

[Raw measurements](x86-64-four-workers-10s.json) include all individual times and hashes.

| Input | Operation | C seconds | Fern seconds | C/Fern speed ratio |
| --- | --- | ---: | ---: | ---: |
| silence | encode | 0.0678 | 0.0521 | 1.300 |
| silence | decode | 0.0110 | 0.0045 | 2.435 |
| tone | encode | 0.1440 | 0.0974 | 1.479 |
| tone | decode | 0.0153 | 0.0149 | 1.029 |
| noise | encode | 0.1488 | 0.1022 | 1.456 |
| noise | decode | 0.0164 | 0.0163 | 1.008 |
| transients | encode | 0.1563 | 0.1040 | 1.503 |
| transients | decode | 0.0168 | 0.0159 | 1.055 |

### One worker, 10-second inputs

[Raw measurements](x86-64-one-worker-10s.json) include all individual times and hashes.

| Input | Operation | C seconds | Fern seconds | C/Fern speed ratio |
| --- | --- | ---: | ---: | ---: |
| silence | encode | 0.0770 | 0.0604 | 1.274 |
| silence | decode | 0.0114 | 0.0046 | 2.460 |
| tone | encode | 0.1660 | 0.1635 | 1.015 |
| tone | decode | 0.0240 | 0.0263 | 0.912 |
| noise | encode | 0.1936 | 0.1798 | 1.077 |
| noise | decode | 0.0183 | 0.0219 | 0.835 |
| transients | encode | 0.1702 | 0.1729 | 0.985 |
| transients | decode | 0.0185 | 0.0231 | 0.799 |

A ratio above 1 means that Fern is faster.
Fern wins every measured four-worker row in these runs.
The short tone and noise decode results are close to a tie.
C remains faster for non-silent one-worker decoding and the one-worker transient encode case.

Each JSON file contains input hashes, executable hashes, encoded sizes, and individual times.
The decode comparison also checks sample differences.
These synthetic inputs cover silence, tones, noise, and transients.
They do not replace tests with speech, music collections, other hardware, or listening panels.
The measurements do not establish equal speed for other quality settings or rate-control modes.

The earlier [single-core baseline](x86-64-simd.json) records the first port before these changes.
Its executable and compiler hashes differ from the current runs.

## Reproduce

Build the reference tools with x86 SIMD enabled:

```sh
git clone --recurse-submodules https://github.com/Aikku93/ulc-codec /tmp/ulc-reference
git -C /tmp/ulc-reference checkout b47df8d50c2ef2d765e5dbec0bf567b5e558173a
git -C /tmp/ulc-reference submodule update --init --recursive
make -C /tmp/ulc-reference -j2 ARCHFLAGS=-D_M_X64
make build
make -C examples/ulc clean all
python3 examples/ulc/bench/run.py \
  --fern examples/ulc/build/ulc --compiler bin/fern \
  --source-commit "$(git rev-parse HEAD)" \
  --reference /tmp/ulc-reference/release \
  --reference-label "C -O2 with x86 SIMD enabled (-D_M_X64)" \
  --seconds 100 --repetitions 11 --cpus 0,1,2,3 --threads 4 \
  --output /tmp/ulc-benchmark.json
```

Select allowed CPUs with `--cpus` on Linux.
Use `--cpu 0 --threads 1` for the one-worker comparison.
Omit both affinity options on systems without CPU affinity support.
Use a clean reference build when you change compiler flags.

## Optimizations

- Four interleaved FFTs share packed float SIMD butterflies.
- Window and overlap operations retain double precision.
- Validated FFT spans use direct SIMD memory operations and preserve retained aliases.
- Window tables cover all overlap scales, including single-sample overlap.
- Silent blocks skip transform work and preserve PCM8 silence bias.
- Numeric zero buffers use bulk copies instead of a push for each element.
- Coefficient priorities use a stable radix sort.
- CBR and ABR measure search candidates without allocation of output bitstreams.
- Decoder noise fill keeps its cursor and random seed in local values.
- Exact matrix powers advance each worker's noise seed.
- Decoder transforms reuse FFT, overlap, and sample buffers across blocks.
- Size-only rate searches skip coefficient quantization when its width is fixed.
- WAV conversion writes into the final output buffer with SIMD float conversion.
- Linux decode workers write disjoint output ranges in one shared mapping.
- Encoder workers use detector checkpoints and exact overlap preroll.

The unit and reference tests cover each optimized codec path.
Serial and parallel output comparisons cover all rate modes and PCM formats.
The memory sanitizer reports zero live allocations after the unit suite and parallel file decode.
