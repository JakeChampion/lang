# Native FLAC fixed-predictor baseline

This comparison measures the first native FLAC encoder and the scalar-return donor fix for #14760.
The host is an AMD EPYC 9V74 running x86-64 Linux.
Each input contains one second of PCM at 48 kHz.
The corpus contains synthetic tones, random noise, and silence. It does not represent a music collection.

The compiler fix removes most temporary record boxes. It does not close the CPU gap to libFLAC 1.5.0.
The timing changes are small and vary in direction. No speed lead or general compression lead is claimed.

## Method

Each process loads input, completes two warmup encodes, then encodes the input 16 times.
The measured interval includes complete streams, PCM MD5, frame CRCs, and a checksum of every output byte.
Input loading, compilation, file writes, and warmup are outside that interval.
Seven trials alternate implementation order on the same pinned CPU.
The table reports median time per encode. The JSON retains every trial and its exact settings.

Both Fern compilers use the same codec and benchmark sources, with production optimization and no debug variables.
libFLAC uses 4096-frame blocks, fixed predictors through order four, partition orders through six, and full stereo search.
LPC is disabled in all implementations. The C wrapper uses the installed libFLAC shared library.
Its exact version and binary hash are recorded. The benchmark verifies each header with a pinned SHA-256 hash.

Each first output passes exact PCM decode in FFmpeg and libFLAC, including PCM MD5 verification.
Both Fern compilers emit identical bytes. Each trial must retain its implementation checksum and output extent.
Frame-byte counts exclude metadata. The libFLAC stream contains 44 more metadata bytes than the Fern stream.

## Native measurements

| Input | Before, ms | After, ms | libFLAC, ms | After/libFLAC | Before allocations | After allocations |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| music-2ch | 62.483 | 61.191 | 1.629 | 37.57 | 80,016 | 19,056 |
| music-8ch | 66.566 | 66.075 | 4.930 | 13.40 | 79,723 | 18,763 |
| noise-2ch | 66.373 | 68.439 | 1.657 | 41.31 | 78,407 | 17,447 |
| silence-2ch | 1.936 | 1.897 | 0.833 | 2.28 | 369 | 369 |

The table divides allocation counts for the same interval by 16.
libFLAC allocation traffic was not instrumented. Fresh-byte counts are heap growth, not total allocated bytes.

| Input | Fern frame bytes | libFLAC frame bytes | Fern native peak, KiB | libFLAC native peak, KiB |
| --- | ---: | ---: | ---: | ---: |
| music-2ch | 60,416 | 62,927 | 2,812 | 1,792 |
| music-8ch | 297,901 | 310,445 | 10,552 | 3,456 |
| noise-2ch | 192,122 | 192,122 | 2,780 | 1,792 |
| silence-2ch | 194 | 194 | 2,732 | 1,536 |

Native peak memory comes from Linux VmHWM inside each process. It excludes the Python launcher.
It includes input conversion and warmup. Different input storage prevents a claim about encoder-state memory alone.
The runtime profile puts most remaining CPU time in Rice search and residual calculation.
Android device, native ARM64, battery, thermal, and music-corpus measurements remain open.

## Reproduce

Install a native C compiler, FFmpeg, and libFLAC 1.5.0.
Build each Fern compiler with `make bin/fern` in its checkout.
Run the same working tree with both compiler paths:

```sh
python3 scripts/bench-flac-encode \
  --compiler /path/to/candidate/bin/fern \
  --baseline-compiler /path/to/baseline/bin/fern \
  --rounds 16 --trials 7 --output report.json
```

The benchmark downloads and verifies the five public libFLAC headers when they are absent.
The JSON records compiler, core, reference library, and source hashes. It also records CPU affinity and the CPU quota.

The preserved result is [x86-64-linux.json](x86-64-linux.json).
Parents: #14515 and #14516. The remaining encoder work is tracked in #14688.
