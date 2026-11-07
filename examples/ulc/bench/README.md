# ULC performance measurements

These measurements compare the complete Fern CLI with the reference C CLI.
They include process startup, file I/O, codec work, and WAV conversion.
They do not establish a worldwide speed or perceptual-quality ranking.

## Recorded run

- Machine: INTEL(R) XEON(R) PLATINUM 8573C.
- Platform: `Linux-6.18.57-x86_64-with-glibc2.41`.
- CPU affinity: CPU 0 for both executables.
- Input: 10 seconds of stereo PCM16 at 44.1 kHz.
- Encoder: VBR quality 75, block size 2048.
- Decoder: the same C-generated ULC2 file for both executables, with FLOAT32 output.
- Method: seven repetitions, with alternate process order.
- Statistic: median wall-clock seconds.
- Fern compiler: the self-hosted compiler built from this checkout.
- Compiler source: `5b33d1a2a90b9d38f469711bf11a7c07a8e2f52b`, unchanged by the codec example.
- Reference: ulc-codec `b47df8d50c2ef2d765e5dbec0bf567b5e558173a` and its pinned libfourier.
- Reference build: GCC `-O2`, with `ARCHFLAGS=-D_M_X64` to enable x86 SIMD.

The upstream libfourier architecture check omits `__x86_64__` for GCC.
The explicit macro enables its existing x86 dispatch and SIMD implementations.
This comparison uses that faster build.

| Input | Operation | C seconds | Fern seconds | C/Fern speed ratio |
| --- | --- | ---: | ---: | ---: | ---: |
| silence | encode | 0.0719 | 0.0482 | 1.492 |
| silence | decode | 0.0128 | 0.0311 | 0.411 |
| tone | encode | 0.1563 | 0.1663 | 0.940 |
| tone | decode | 0.0186 | 0.0524 | 0.355 |
| noise | encode | 0.1677 | 0.1782 | 0.941 |
| noise | decode | 0.0171 | 0.0465 | 0.369 |
| transients | encode | 0.1614 | 0.1682 | 0.960 |
| transients | decode | 0.0187 | 0.0501 | 0.373 |

A ratio above 1 means that Fern is faster.
Fern encodes silence faster in this run.
The non-silent encoder results are close to C, with C ahead.
C decodes all four inputs faster.
The implementation therefore does not meet an across-the-board fastest claim.

The JSON file contains all individual times, input hashes, and executable hashes.
The decode comparison also checks sample differences.
These synthetic inputs cover silence, tones, noise, and transients.
They do not replace tests with speech, music collections, other hardware, or listening panels.

## Reproduce

Build the reference tools with x86 SIMD enabled:

```sh
git clone --recurse-submodules https://github.com/Aikku93/ulc-codec /tmp/ulc-reference
git -C /tmp/ulc-reference checkout b47df8d50c2ef2d765e5dbec0bf567b5e558173a
git -C /tmp/ulc-reference submodule update --init --recursive
make -C /tmp/ulc-reference -j2 ARCHFLAGS=-D_M_X64
make -C examples/ulc
python3 examples/ulc/bench/run.py \
  --fern examples/ulc/build/ulc --compiler bin/fern \
  --reference /tmp/ulc-reference/release \
  --reference-label "C -O2 with x86 SIMD enabled (-D_M_X64)" \
  --seconds 10 --repetitions 7 --cpu 0 \
  --output /tmp/ulc-benchmark.json
```

Select an allowed CPU with `--cpu` on Linux.
Omit `--cpu` on systems without CPU affinity support.
Use a clean reference build when you change compiler flags.

## Optimizations

- DCT-IV uses an N/2-point FFT, with cached rotations and SIMD butterfly operations.
- Window tables cover all overlap scales, including single-sample overlap.
- Silent transforms skip FFT work.
- Numeric zero buffers use bulk copies instead of a push for each element.
- Coefficient priorities use a stable radix sort.
- CBR and ABR measure search candidates without allocation of output bitstreams.
- Decoder noise fill keeps its cursor and random seed in local values.
- Decoder transforms reuse FFT and sample buffers across blocks.
- Size-only rate searches skip coefficient quantization when its width is fixed.
- WAV conversion uses fixed-width operations for common sample formats.

The unit and reference tests cover each optimized codec path.
The memory sanitizer reports zero live allocations after the unit suite.
