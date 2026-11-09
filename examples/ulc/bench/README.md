# ULC benchmarks

These FLOAT32/VBR measurements date from 2026-11-08 on an Intel Xeon Platinum 8573C with a four-CPU container quota.
The FLOAT32/VBR suite records 131 lower Fern medians across 144 comparisons against tuned upstream C.
The geometric mean of C/Fern ratios is 1.374×, with equal weights for all cases.
Without silence, the ratio is 1.253× across 108 cases.
Several cases still favor C, particularly large-block noise decode.
These results do not establish a universal performance ranking.

## Constant masks and narrow PCM bias

Source `fa46bf036c3a1822289f37ef79ae776d6bb40325` folds constant bitwise masks before branch removal.
PCM8 and PCM16 conversion uses the existing float bias directly to pack integers.
Exact half-integers retain the double-precision fallback, and PCM24 retains double-precision conversion.
The baseline source is `93ef6fb03b1863fac6f1ef155bd8af47a9322f82`.
Each codec uses the release compiler and standard library from its own pinned revision.
The report compares the compiler and codec changes together.

The 2026-11-09 comparison uses CPU 0 of the Xeon Platinum 8573C and one worker.
Its 24 cases cover silence, tone, noise, and transients in all three PCM depths.
Ten-second inputs use block size 32768, and hundred-second inputs use block size 2048.
Every case has thirty repetitions in six balanced execution orders.
Timing includes process startup and file I/O, with no other local builds or tests.
Every final WAV equals the baseline byte for byte, and all six float64 oracle checks pass.

Fern has lower medians than the baseline in 19 of 24 cases.
The geometric-mean baseline/Fern ratio is 1.055× overall and 1.076× without silence.
The corresponding fixed-suite intervals are 1.023× to 1.086× and 1.053× to 1.105×.
The raw report retains all five baseline regressions.

Fern has lower medians than tuned C in 21 of 24 cases.
The geometric-mean C/Fern ratio is 1.654× overall and 1.193× without silence.
The corresponding intervals are 1.619× to 1.696× and 1.170× to 1.225×.
C remains faster in the three large-block noise cases.
Their C/Fern ratios are 0.958× for PCM8, 0.968× for PCM16, and 0.891× for PCM24.
These results do not establish a universal speed ranking.

The intervals use 2000 paired bootstrap repetitions and seed 417.
They cover repetition uncertainty within these fixed cases, without claims for other inputs or machines.

| Seconds | Block size | Output | Baseline/Fern | C/Fern |
| ---: | ---: | --- | ---: | ---: |
| 10 | 32768 | PCM8 | 0.970 | 1.554 |
| 10 | 32768 | PCM16 | 1.087 | 1.527 |
| 10 | 32768 | PCM24 | 1.004 | 1.360 |
| 100 | 2048 | PCM8 | 1.051 | 2.092 |
| 100 | 2048 | PCM16 | 1.175 | 1.980 |
| 100 | 2048 | PCM24 | 1.057 | 1.534 |

[The complete report](x86-64-mask-bias.json) retains every repetition, regression, executable hash, compiler hash, and oracle error.
Its `build_provenance` identifies both release compilers and the independent oracle.

Build both pinned releases:

```sh
git worktree add --detach /tmp/ulc-mask-source fa46bf036c3a1822289f37ef79ae776d6bb40325
git worktree add --detach /tmp/ulc-mask-baseline 93ef6fb03b1863fac6f1ef155bd8af47a9322f82
make -C /tmp/ulc-mask-source build
make -C /tmp/ulc-mask-baseline build
make -C /tmp/ulc-mask-source/examples/ulc FERN=/tmp/ulc-mask-source/bin/fern build/ulc
make -C /tmp/ulc-mask-baseline/examples/ulc FERN=/tmp/ulc-mask-baseline/bin/fern build/ulc
```

Use the pinned C build from [the FLOAT32/VBR reproduction section](#reproduce-the-float32-and-vbr-suite).
Use the pinned oracle from [the widening-load reproduction below](#widening-loads-for-pcm24).

```sh
python3 /tmp/ulc-mask-source/examples/ulc/bench/run.py \
  --fern /tmp/ulc-mask-source/examples/ulc/build/ulc \
  --baseline-fern /tmp/ulc-mask-baseline/examples/ulc/build/ulc \
  --baseline-source-commit 93ef6fb03b1863fac6f1ef155bd8af47a9322f82 \
  --compiler /tmp/ulc-mask-source/bin/fern \
  --source-commit fa46bf036c3a1822289f37ef79ae776d6bb40325 \
  --oracle /tmp/ulc-load-source/examples/ulc/build/reference64 \
  --reference /tmp/ulc-reference/release \
  --reference-label 'GCC -O3 -march=native -fno-math-errno -funroll-loops with x86 SIMD (-D_M_X64)' \
  --direction decode --seconds 10 --block-size 32768 \
  --output-format PCM8 --repetitions 30 --cpu 0 \
  --output /tmp/ulc-mask-10-PCM8.json
```

Repeat for PCM16 and PCM24, then for 100 seconds at block size 2048.
Use separate output paths and summarize all six reports with `summarize.py`.

## Widening loads for PCM24

Source `9f5ed23c4bf793684d1611ae9171a3eee87c3780` loads two packed floats directly into double lanes during PCM24 conversion.
The same double-precision reconstruction, clipping, rounding, and output bytes apply.
Both codec builds use compiler `9f5ed23c4bf793684d1611ae9171a3eee87c3780` and its standard library.
The baseline codec source is `78971d38d86deaeba246990a4ee00108fc84df33`.

The measurements date from 2026-11-09 on CPU 0 of the Xeon Platinum 8573C.
The suite uses one worker, stereo PCM16 input at 44.1 kHz, and four fixtures: silence, tone, noise, and transients.
Ten-second inputs use block size 32768, and hundred-second inputs use block size 2048.

All three PCM output depths have thirty repetitions, with six balanced execution orders for C, Fern, and the baseline.
Timing includes process startup and file I/O.
No other local builds or tests run during measurement.

PCM24 has lower medians than the baseline in all eight cases.
Its geometric-mean baseline/Fern ratio is 1.061×, or 1.069× across the six non-silent cases.
The corresponding fixed-suite intervals are 1.011× to 1.114× and 1.034× to 1.118×.

The complete 24-case comparison has fifteen lower Fern medians than the baseline.
Its geometric-mean baseline/Fern ratio is 1.028×, with an interval of 1.000× to 1.054×.
The raw report retains nine baseline regressions in PCM8 or PCM16.

Fern has lower medians than tuned C in 18 of 24 cases.
The geometric-mean C/Fern ratio is 1.592× overall and 1.115× without silence.
The corresponding fixed-suite intervals are 1.558× to 1.630× and 1.094× to 1.148×.
All nine long non-silent cases favor Fern.
Six large-block cases still favor C, so these results do not establish a universal speed ranking.

The calculations use 2000 paired bootstrap repetitions and seed 417.
The intervals cover repetition uncertainty within these fixed cases.
They do not quantify other inputs, machines, or runs.

| Seconds | Block size | Output | Baseline/Fern | C/Fern |
| ---: | ---: | --- | ---: | ---: |
| 10 | 32768 | PCM8 | 0.999 | 1.659 |
| 10 | 32768 | PCM16 | 1.025 | 1.436 |
| 10 | 32768 | PCM24 | 1.057 | 1.260 |
| 100 | 2048 | PCM8 | 1.027 | 2.065 |
| 100 | 2048 | PCM16 | 0.994 | 1.784 |
| 100 | 2048 | PCM24 | 1.065 | 1.474 |

[The complete report](x86-64-widen-loads.json) includes every repetition, regression, executable hash, compiler hash, and oracle error.
Every final WAV matches the baseline byte for byte, and all six float64 oracle checks pass.

Build the pinned compiler, decoder, and oracle:

```sh
git worktree add --detach /tmp/ulc-load-source 9f5ed23c4bf793684d1611ae9171a3eee87c3780
make -C /tmp/ulc-load-source build
make -C /tmp/ulc-load-source/examples/ulc \
  FERN=/tmp/ulc-load-source/bin/fern build/ulc build/reference64
git worktree add --detach /tmp/ulc-load-baseline 78971d38d86deaeba246990a4ee00108fc84df33
make -C /tmp/ulc-load-baseline/examples/ulc \
  FERN=/tmp/ulc-load-source/bin/fern \
  FERNFLAGS="-stdlib /tmp/ulc-load-source/internal/stdlib" build/ulc
```

Use the pinned C build in [the FLOAT32/VBR reproduction section](#reproduce-the-float32-and-vbr-suite).
Run each output format at both durations:

```sh
python3 /tmp/ulc-load-source/examples/ulc/bench/run.py \
  --fern /tmp/ulc-load-source/examples/ulc/build/ulc \
  --baseline-fern /tmp/ulc-load-baseline/examples/ulc/build/ulc \
  --baseline-source-commit 78971d38d86deaeba246990a4ee00108fc84df33 \
  --compiler /tmp/ulc-load-source/bin/fern \
  --source-commit 9f5ed23c4bf793684d1611ae9171a3eee87c3780 \
  --oracle /tmp/ulc-load-source/examples/ulc/build/reference64 \
  --reference /tmp/ulc-reference/release \
  --reference-label 'GCC -O3 -march=native -fno-math-errno -funroll-loops with x86 SIMD (-D_M_X64)' \
  --direction decode --seconds 10 --block-size 32768 \
  --output-format PCM24 --repetitions 30 --cpu 0 \
  --output /tmp/ulc-load-10-PCM24.json
```

Repeat for PCM8 and PCM16, then for 100 seconds at block size 2048.
Use separate output paths for each configuration.
Summarize the complete suite and the PCM24 baseline comparison:

```sh
python3 examples/ulc/bench/summarize.py \
  --output /tmp/ulc-load-c.json /tmp/ulc-load-[0-9]*-PCM*.json
python3 examples/ulc/bench/summarize.py --comparison Baseline \
  --output /tmp/ulc-load-baseline.json /tmp/ulc-load-[0-9]*-PCM*.json
python3 examples/ulc/bench/summarize.py --comparison Baseline \
  --output /tmp/ulc-load-pcm24.json /tmp/ulc-load-[0-9]*-PCM24.json
```

## Final decoder comparison

Source `466f99efe3296308be61943985d395100ef25990` combines all three decoder changes below.
The same binary serves every case in this final comparison.
Both Fern binaries use compiler source `acbe1f2f38b76dddf0db952f11bba1e73876e04b`.
The baseline source is `49056c6e392ba85b267e4abb6a44dd94872cc891`.

The 24 cases cover four fixtures, three PCM depths, and two input durations.
Ten-second inputs use block size 32768.
Hundred-second inputs use block size 2048.

Each case has thirty repetitions, with one worker on CPU 0 of the Xeon Platinum 8573C.
Six execution orders give C, Fern, and the baseline each position equally.
Timing includes process startup and file I/O.
No other local builds or tests run during measurement.

Fern has lower medians than the baseline in 21 of 24 cases, including all eighteen non-silent cases.
The geometric-mean baseline/Fern ratio is 1.166×, or 1.247× without silence.
The corresponding fixed-suite intervals are 1.141× to 1.189× and 1.221× to 1.268×.
Three short silence cases have lower baseline medians.
The raw report retains those regressions.

Fern has lower medians than tuned native C in 18 of 24 cases.
The geometric-mean C/Fern ratio is 1.566×, or 1.110× without silence.
The corresponding fixed-suite intervals are 1.532× to 1.590× and 1.086× to 1.124×.
The calculations use 2000 paired bootstrap repetitions and seed 417.
These intervals cover repetition uncertainty in this fixed suite.
They do not quantify other inputs, machines, or runs.

| Seconds | Block size | Output | Baseline/Fern | C/Fern |
| ---: | ---: | --- | ---: | ---: |
| 10 | 32768 | PCM8 | 1.083 | 1.526 |
| 10 | 32768 | PCM16 | 1.154 | 1.454 |
| 10 | 32768 | PCM24 | 1.081 | 1.250 |
| 100 | 2048 | PCM8 | 1.257 | 2.004 |
| 100 | 2048 | PCM16 | 1.319 | 1.855 |
| 100 | 2048 | PCM24 | 1.124 | 1.428 |

[The complete final report](x86-64-decoder-final.json) includes all repetitions, executable hashes, compiler hashes, and oracle errors.
Every final WAV equals the original baseline byte for byte.
All six configurations pass the independent float64 oracle checks.
C remains faster in six cases, so this report does not establish a universal speed ranking.

Build the pinned compiler, codec, and original baseline with the commands in the sections below.
Run each output format at both durations:

```sh
python3 examples/ulc/bench/run.py \
  --fern /tmp/ulc-pcm24-source/examples/ulc/build/ulc \
  --baseline-fern /tmp/ulc-fft-before/examples/ulc/build/ulc \
  --baseline-source-commit 49056c6e392ba85b267e4abb6a44dd94872cc891 \
  --compiler /tmp/ulc-fft-compiler/bin/fern \
  --source-commit 466f99efe3296308be61943985d395100ef25990 \
  --oracle /tmp/ulc-pcm24-source/examples/ulc/build/reference64 \
  --reference /tmp/ulc-reference/release \
  --direction decode --seconds 10 --block-size 32768 \
  --output-format PCM16 --repetitions 30 --cpu 0 \
  --output /tmp/ulc-decoder-10-PCM16.json
```

Repeat for PCM8 and PCM24, then for 100 seconds at block size 2048.
Use separate output paths for each configuration.
Summarize the complete suite:

```sh
python3 examples/ulc/bench/summarize.py --output /tmp/ulc-decoder-c.json /tmp/ulc-decoder-[0-9]*-PCM*.json
python3 examples/ulc/bench/summarize.py --comparison Baseline \
  --output /tmp/ulc-decoder-baseline.json /tmp/ulc-decoder-[0-9]*-PCM*.json
```

## PCM24 word stores

Source `466f99efe3296308be61943985d395100ef25990` converts four PCM24 samples with double-precision SIMD and three word stores.
The arithmetic, clipping, and integer samples match the prior implementation.
The scalar-oracle tests now cover 884736 random and boundary samples across all three PCM depths.

The baseline source is `86e27cfe596f23167f57adfa74e158b93dd76363`.
Both binaries use compiler source `acbe1f2f38b76dddf0db952f11bba1e73876e04b`.
The eight stereo cases have thirty repetitions each on CPU 0, with one worker and six balanced execution orders.
No other local builds or tests run during measurement.
Every final WAV matches the baseline byte for byte, and all float64 oracle checks pass.

The newer build has lower medians in all six non-silent cases.
Its geometric-mean baseline/Fern ratio is 1.059× for those cases, with a fixed-suite interval of 1.023× to 1.100×.
The aggregate across all eight cases is 1.024×, with an interval of 0.990× to 1.063×.
The full aggregate does not establish a gain because its interval includes 1.0.
Both intervals use 2000 paired bootstrap repetitions and seed 417.

| Seconds | Input | Baseline/Fern | C/Fern |
| ---: | --- | ---: | ---: |
| 10 | silence | 0.957 | 3.586 |
| 10 | tone | 1.147 | 0.943 |
| 10 | noise | 1.013 | 0.804 |
| 10 | transients | 1.009 | 0.918 |
| 100 | silence | 0.898 | 2.907 |
| 100 | tone | 1.103 | 1.159 |
| 100 | noise | 1.061 | 0.973 |
| 100 | transients | 1.028 | 1.063 |

[The complete report](x86-64-pcm24-stores.json) retains every repetition, error, and hash.
Fern has lower medians than C in four of the eight cases.
The C/Fern aggregate is 1.311×, or 0.970× without silence.
C remains faster on short non-silent inputs and the long noise input.

Build the compiler and narrow-PCM baseline with the commands in the sections below.
Build the newer codec with that same compiler:

```sh
git worktree add /tmp/ulc-pcm24-source 466f99efe3296308be61943985d395100ef25990
make -C /tmp/ulc-pcm24-source/examples/ulc FERN=/tmp/ulc-fft-compiler/bin/fern
make -C /tmp/ulc-pcm24-source/examples/ulc build/reference64 FERN=/tmp/ulc-fft-compiler/bin/fern
python3 examples/ulc/bench/run.py \
  --fern /tmp/ulc-pcm24-source/examples/ulc/build/ulc \
  --baseline-fern /tmp/ulc-narrow-source/examples/ulc/build/ulc \
  --baseline-source-commit 86e27cfe596f23167f57adfa74e158b93dd76363 \
  --compiler /tmp/ulc-fft-compiler/bin/fern \
  --source-commit 466f99efe3296308be61943985d395100ef25990 \
  --oracle /tmp/ulc-pcm24-source/examples/ulc/build/reference64 \
  --reference /tmp/ulc-reference/release \
  --direction decode --seconds 10 --block-size 32768 \
  --output-format PCM24 --repetitions 30 --cpu 0 \
  --output /tmp/ulc-pcm24-10.json
```

Repeat with `--seconds 100 --block-size 2048 --output /tmp/ulc-pcm24-100.json`.
Summarize both comparisons:

```sh
python3 examples/ulc/bench/summarize.py --output /tmp/ulc-pcm24-c.json /tmp/ulc-pcm24-[0-9]*.json
python3 examples/ulc/bench/summarize.py --comparison Baseline \
  --output /tmp/ulc-pcm24-baseline.json /tmp/ulc-pcm24-[0-9]*.json
```

## Narrow PCM conversion

Source `86e27cfe596f23167f57adfa74e158b93dd76363` combines contiguous FFT stores with four-lane PCM8 and PCM16 conversion.
Exact half-integer float results use the original double-precision conversion.
This fallback preserves integer samples near each rounding boundary.
The tests compare 589824 random and boundary samples with the independent scalar writer.

The comparison uses the same compiler, C build, CPU, input types, and baseline revision as the packed FFT report below.
Each of its 24 cases has thirty repetitions in six balanced execution orders.
No other local builds or tests run during measurement.
Every final WAV equals the baseline byte for byte, and all float64 oracle checks pass.

Fern has lower medians than the baseline in 21 of 24 cases.
The geometric-mean baseline/Fern ratio is 1.172×, or 1.245× without silence.
The fixed-suite bootstrap interval is 1.145× to 1.200×.
The calculation uses 2000 repetitions and seed 417.
This interval covers repetition uncertainty within these cases.

Fern has lower medians than C in 18 of 24 cases.
The aggregate C/Fern ratio is 1.523×, or 1.085× without silence.
The fixed-suite interval for the aggregate C comparison is 1.488× to 1.554×.
C remains faster in six cases.
These measurements do not establish a universal speed ranking.

| Seconds | Block size | Output | Baseline/Fern | C/Fern |
| ---: | ---: | --- | ---: | ---: |
| 10 | 32768 | PCM16 | 1.164 | 1.460 |
| 10 | 32768 | PCM24 | 1.116 | 1.204 |
| 10 | 32768 | PCM8 | 1.154 | 1.506 |
| 100 | 2048 | PCM16 | 1.253 | 1.741 |
| 100 | 2048 | PCM24 | 1.078 | 1.341 |
| 100 | 2048 | PCM8 | 1.280 | 2.017 |

[The complete report](x86-64-narrow-pcm.json) retains all individual times, hashes, regressions, and oracle errors.
Its `baseline_summary` records the previous Fern comparison separately.
The baseline revision is `49056c6e392ba85b267e4abb6a44dd94872cc891`.
The compiler revision is `acbe1f2f38b76dddf0db952f11bba1e73876e04b`.

Build the compiler and baseline with the commands in the packed FFT section below.
Build the newer codec with that same compiler:

```sh
git worktree add /tmp/ulc-narrow-source 86e27cfe596f23167f57adfa74e158b93dd76363
make -C /tmp/ulc-narrow-source/examples/ulc FERN=/tmp/ulc-fft-compiler/bin/fern
make -C /tmp/ulc-narrow-source/examples/ulc build/reference64 FERN=/tmp/ulc-fft-compiler/bin/fern
python3 examples/ulc/bench/run.py \
  --fern /tmp/ulc-narrow-source/examples/ulc/build/ulc \
  --baseline-fern /tmp/ulc-fft-before/examples/ulc/build/ulc \
  --baseline-source-commit 49056c6e392ba85b267e4abb6a44dd94872cc891 \
  --compiler /tmp/ulc-fft-compiler/bin/fern \
  --source-commit 86e27cfe596f23167f57adfa74e158b93dd76363 \
  --oracle /tmp/ulc-narrow-source/examples/ulc/build/reference64 \
  --reference /tmp/ulc-reference/release \
  --direction decode --seconds 10 --block-size 32768 \
  --output-format PCM16 --repetitions 30 --cpu 0 \
  --output /tmp/ulc-narrow-10-PCM16.json
```

Repeat for PCM8 and PCM24, then for 100 seconds at block size 2048.
Use separate output paths for each configuration.
Use `summarize.py --comparison Baseline` to reproduce the baseline aggregate.

## Packed FFT output

Source `aadbb7a969b682269117759f8cb46af11db5594f` writes the packed FFT output with contiguous SIMD stores.
It reuses the existing FFT scratch spans for the final rotations and transposes four adjacent groups.
The change adds no allocation and preserves all output bytes.
This source precedes the separate narrow PCM conversion change.

The comparison uses baseline source `49056c6e392ba85b267e4abb6a44dd94872cc891`.
Both binaries use compiler source `acbe1f2f38b76dddf0db952f11bba1e73876e04b` and the same build flags.
Each of the 24 cases has 30 repetitions on CPU 0, with one worker.
Six execution orders give all three decoders each position equally.
The inputs use stereo PCM16 at 44.1 kHz.
No other local builds or tests run during measurement.

Fern has lower medians than the baseline in 19 of 24 cases.
The geometric-mean baseline/Fern ratio is 1.051×, or 1.084× without silence.
The fixed-suite bootstrap interval is 1.027× to 1.082×.
The calculation uses 2000 repetitions and seed 417.
The interval covers repetition uncertainty within these cases.
It does not quantify other inputs, machines, or runs.

| Seconds | Block size | Output | Baseline/Fern | C/Fern |
| ---: | ---: | --- | ---: | ---: |
| 10 | 32768 | PCM16 | 1.006 | 1.313 |
| 10 | 32768 | PCM24 | 1.017 | 1.192 |
| 10 | 32768 | PCM8 | 1.000 | 1.321 |
| 100 | 2048 | PCM16 | 1.082 | 1.546 |
| 100 | 2048 | PCM24 | 1.121 | 1.340 |
| 100 | 2048 | PCM8 | 1.090 | 1.733 |

[The complete report](x86-64-post-stores.json) retains every timing, regression, executable hash, and oracle error.
Every final WAV equals the baseline byte for byte.
The float64 oracle checks frame counts and sample errors outside the timed commands.

Fern has lower medians than C in 13 of 24 cases.
The aggregate C/Fern ratio is 1.397×.
The ratio without silence is 0.955×.
C remains faster in eleven cases.

Build the pinned compiler and both codec revisions:

```sh
git worktree add /tmp/ulc-fft-compiler acbe1f2f38b76dddf0db952f11bba1e73876e04b
make -C /tmp/ulc-fft-compiler build
git worktree add /tmp/ulc-fft-source aadbb7a969b682269117759f8cb46af11db5594f
git worktree add /tmp/ulc-fft-before 49056c6e392ba85b267e4abb6a44dd94872cc891
make -C /tmp/ulc-fft-source/examples/ulc FERN=/tmp/ulc-fft-compiler/bin/fern
make -C /tmp/ulc-fft-source/examples/ulc build/reference64 FERN=/tmp/ulc-fft-compiler/bin/fern
make -C /tmp/ulc-fft-before/examples/ulc FERN=/tmp/ulc-fft-compiler/bin/fern
for output in PCM8 PCM16 PCM24; do
  python3 examples/ulc/bench/run.py \
    --fern /tmp/ulc-fft-source/examples/ulc/build/ulc \
    --baseline-fern /tmp/ulc-fft-before/examples/ulc/build/ulc \
    --baseline-source-commit 49056c6e392ba85b267e4abb6a44dd94872cc891 \
    --compiler /tmp/ulc-fft-compiler/bin/fern \
    --source-commit aadbb7a969b682269117759f8cb46af11db5594f \
    --oracle /tmp/ulc-fft-source/examples/ulc/build/reference64 \
    --reference /tmp/ulc-reference/release \
    --direction decode --seconds 10 --block-size 32768 \
    --output-format "$output" --repetitions 30 --cpu 0 \
    --output "/tmp/ulc-fft-10-$output.json"
done
```

Repeat with `--seconds 100 --block-size 2048` and separate output paths.
Use the native C build flags in the reproduction section below.
Summarize C or the baseline as the selected comparison:

```sh
python3 examples/ulc/bench/summarize.py --output /tmp/ulc-fft-c.json /tmp/ulc-fft-[0-9]*-PCM*.json
python3 examples/ulc/bench/summarize.py --comparison Baseline \
  --output /tmp/ulc-fft-baseline.json /tmp/ulc-fft-[0-9]*-PCM*.json
```

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

## CBR and ABR encoding

Source `7c91179ef43de2ac1622d06d0f7885fea9095fb2` specializes bitstream size calculations.
It keeps the existing byte writer separate from the count-only helpers.
The suite compares CBR and ABR at 96 kbps with tuned upstream C.
Inputs use stereo PCM16 at 44.1 kHz and block size 2048.
Both encoders receive the same average complexity for ABR, measured by C outside timing.

The 32 cases cover four inputs, two durations, both rate modes, and one or four workers.
Each case has eleven alternating repetitions, with startup and file I/O included.
No other local builds or tests run during timing.

Fern has lower medians in 31 of 32 cases.
The geometric-mean C/Fern ratio is 1.376× overall and 1.391× without silence.
The fixed-suite bootstrap interval is 1.356× to 1.395×, with 2000 repetitions and seed 417.
The remaining loss is ten-second tone input in one-worker ABR, at 0.981×.
These results do not establish a universal ranking.

Each configuration below contains four input types:

| Duration | Mode | One-worker wins | One-worker geometric mean | Four-worker wins | Four-worker geometric mean |
| ---: | --- | ---: | ---: | ---: | ---: |
| 10 s | CBR | 4/4 | 1.143 | 4/4 | 1.704 |
| 10 s | ABR | 3/4 | 1.127 | 4/4 | 1.793 |
| 100 s | CBR | 4/4 | 1.088 | 4/4 | 1.717 |
| 100 s | ABR | 4/4 | 1.085 | 4/4 | 1.614 |

[All eight raw reports](x86-64-rate-modes.json) retain the individual times, hashes, file sizes, RMS errors, and SNR values.
The float64 decoder checks frame counts and finite samples outside timing.
Quality calculations omit the two-block delay and final padding.
File bitrate includes container and padding bytes.

Fern and C file sizes differ by at most 0.085% in this suite.
Fern's SNR difference from C ranges from about -0.000001 dB to +0.024337 dB.
All 32 file sizes and Fern RMS errors match the prior implementation's recorded values.

[The exploratory before-and-after probes](x86-64-bitstream-before-after.json) retain all nine cases and 31 alternating repetitions per case.
Their final encoded files match byte for byte in VBR, CBR, and ABR.
The candidate binary matches the committed build byte for byte.
These probes show a 1.026× geometric-mean improvement over the previous writer.
Individual probe results include gains and small losses.

Build the pinned source in a separate worktree:

```sh
git worktree add /tmp/ulc-count-measured 7c91179ef43de2ac1622d06d0f7885fea9095fb2
make -C /tmp/ulc-count-measured build
make -C /tmp/ulc-count-measured/examples/ulc
make -C /tmp/ulc-count-measured/examples/ulc build/reference64
python3 /tmp/ulc-count-measured/examples/ulc/bench/run.py \
  --fern /tmp/ulc-count-measured/examples/ulc/build/ulc \
  --compiler /tmp/ulc-count-measured/bin/fern \
  --oracle /tmp/ulc-count-measured/examples/ulc/build/reference64 \
  --source-commit 7c91179ef43de2ac1622d06d0f7885fea9095fb2 \
  --reference /tmp/ulc-reference/release \
  --reference-label 'GCC -O3 -march=native -fno-math-errno -funroll-loops with x86 SIMD (-D_M_X64)' \
  --rate-mode cbr --bitrate 96 --direction encode \
  --seconds 10 --repetitions 11 --cpu 0 --threads 1 \
  --output /tmp/ulc-rate-report-10-cbr-one.json
```

Use `--rate-mode abr` for ABR, and repeat with 100-second inputs.
Use `--cpus 0,1,2,3 --threads 4` for four-worker measurements.
Reproduce the aggregate with `bench/summarize.py`:

```sh
python3 examples/ulc/bench/summarize.py \
  --output /tmp/ulc-rate-summary.json /tmp/ulc-rate-report-*.json
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
