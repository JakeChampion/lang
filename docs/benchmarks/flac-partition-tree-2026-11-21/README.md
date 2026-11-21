# Exact FLAC partition planning

The planner now scans folded residuals once to obtain sums and bit widths at the finest legal partition order. It combines those summaries at coarser orders, preserving the exact quotient scans and tie rules. The fixed and LPC searches each reuse a predictor scratch array capped at 512 KiB for the largest partition count.

The comparison uses the same compiler for the new planner and the prior standard library at `32748e79`. Each input has 48,000 frames. Seven trials alternate execution order on one x86-64 Linux CPU. The timed interval includes four encodes and a checksum after two warmups. Whole-process CPU and peak RSS also include input loading, warmups and teardown. All candidate outputs matched prior Fern byte for byte. FFmpeg and libFLAC independently decoded the outputs and the reference encoder to exact PCM.

Each group of three values is in **new Fern / prior Fern / libFLAC 1.5.0** order. CPU and RSS are process medians. Wall ratios are medians of paired new/prior trials. Fern counts runtime allocations during four encodes. The C counts include successful glibc malloc, calloc and realloc calls in four additional encodes, measured separately from timing by subtracting the four-round profile from the eight-round profile. The two runtimes count allocations at different layers.

## LPC order 32

| Input | Wall ratio | CPU (ms) | Allocations | Peak RSS (KiB) | Stream bytes |
| --- | ---: | --- | --- | --- | --- |
| music-2ch | 0.847 | 2,016.5 / 2,460.8 / 232.0 | 119,304 / 126,156 / 192 | 1,224 / 1,220 / 1,792 | 59,925 / 59,925 / 61,387 |
| music-8ch | 0.815 | 2,036.4 / 2,471.5 / 481.3 | 117,044 / 123,744 / 364 | 6,048 / 6,048 / 3,456 | 296,980 / 296,980 / 309,846 |
| noise-2ch | 0.827 | 2,020.9 / 2,462.2 / 222.9 | 99,740 / 113,180 / 188 | 1,504 / 1,504 / 1,940 | 192,164 / 192,164 / 192,208 |
| silence-2ch | 1.057 | 7.6 / 7.4 / 6.2 | 996 / 996 / 176 | 968 / 968 / 1,560 | 236 / 236 / 280 |
| music-32bit-2ch | 0.802 | 2,012.1 / 2,499.0 / 287.6 | 121,020 / 127,144 / 188 | 1,508 / 1,508 / 1,920 | 143,240 / 143,240 / 146,959 |
| noise-32bit-2ch | 0.966 | 407.7 / 423.4 / 56.6 | 74,108 / 74,012 / 192 | 1,832 / 1,832 / 2,176 | 384,164 / 384,164 / 384,208 |

## Fixed predictors

| Input | Wall ratio | CPU (ms) | Allocations | Peak RSS (KiB) | Stream bytes |
| --- | ---: | --- | --- | --- | --- |
| music-2ch | 0.687 | 135.5 / 196.0 / 11.7 | 24,564 / 19,636 / 184 | 1,140 / 1,140 / 1,792 | 60,458 / 60,458 / 63,013 |
| music-8ch | 0.758 | 190.4 / 247.7 / 32.1 | 22,356 / 17,240 / 356 | 6,040 / 6,040 / 3,456 | 297,943 / 297,943 / 310,531 |
| noise-2ch | 0.675 | 132.7 / 190.9 / 10.7 | 5,276 / 6,812 / 180 | 1,412 / 1,412 / 1,792 | 192,164 / 192,164 / 192,208 |
| silence-2ch | 1.029 | 7.6 / 7.4 / 6.3 | 900 / 900 / 168 | 956 / 956 / 1,532 | 236 / 236 / 280 |
| music-32bit-2ch | 0.694 | 144.7 / 204.2 / 14.8 | 26,236 / 20,568 / 180 | 1,412 / 1,412 / 1,792 | 144,284 / 144,284 / 148,368 |
| noise-32bit-2ch | 0.908 | 59.5 / 67.0 / 13.4 | 3,356 / 3,212 / 184 | 1,752 / 1,752 / 2,176 | 384,164 / 384,164 / 384,208 |

LPC process CPU fell 17–20% on the three music inputs and 16-bit noise. Full-range 32-bit noise took about 4% less CPU and allocated 96 more times per four encodes. Silence used the same allocation count and about 3% more CPU. On non-silent inputs Fern still took 4.2–9.1 times libFLAC’s process CPU.

## Correctness and reproduction

The 81 fixed and 70 LPC frozen streams retained their bytes, including the 33-bit stereo side-channel control. Both generator campaigns independently verified all PCM and MD5 values with FFmpeg and libFLAC. The cost oracle, boundary, smoke and reference suites passed native sanitizer and leak checks, WASI under Wasmtime, and ARM64 Linux and Android executables under QEMU Linux user-mode. Each suite also compiled as an Android ARM64 shared library. The four bounded interpreter suites produced identical TAP with the Go and self-host interpreters.

Build the self-host compiler, install libFLAC 1.5.0 and FFmpeg, and create a separate checkout at `32748e79`. Run from the candidate checkout with `prior` set to that checkout’s absolute path.

```sh
out=docs/benchmarks/flac-partition-tree-2026-11-21
scripts/bench-flac-encode --compiler bin/fern-selfhost --baseline-stdlib "$prior/internal/stdlib" \
  --wide --rounds 4 --trials 7 --output /tmp/fixed.json
scripts/bench-flac-encode --compiler bin/fern-selfhost --baseline-stdlib "$prior/internal/stdlib" \
  --lpc-order 32 --baseline-lpc-order 32 --wide --rounds 4 --trials 7 --output /tmp/lpc.json
cc -O3 -Wall -Wextra -Werror -I build/audio/flac-headers -o /tmp/flac-reference \
  scripts/audio-reference/flac_bench.c -l:libFLAC.so.14
for mode in fixed lpc; do
  python3 "$out/reference-allocations.py" /tmp/flac-reference "/tmp/$mode.json" > "/tmp/$mode-allocations.json"
done
```

The raw reports retain compiler and codec hashes, individual trials, output sizes, PCM input and encoded stream hashes, CPU model and affinity. [LPC trials](lpc.json) · [Fixed trials](fixed.json) · [LPC reference allocations](lpc-reference-allocations.json) · [Fixed reference allocations](fixed-reference-allocations.json)
