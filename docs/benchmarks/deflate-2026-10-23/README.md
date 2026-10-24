# std/deflate against libdeflate, baseline

This report is the starting point for the work on std/deflate. It compares
the pure-Fern encoder and decoder with libdeflate 1.24, which is written in C,
on the Silesia corpus. It uses the same method as
[rtfeldman.com/fp-vs-c](https://rtfeldman.com/fp-vs-c), which compares a
pure-Roc DEFLATE with libdeflate.

`scripts/deflate-bench` makes these measurements again. `results-arm64-darwin.csv`
holds the rows that this report summarises.

## Setup

- Host: Apple M3 Pro, macOS, target `arm64-darwin`.
- Fern: std/deflate as of 020f5be85, built by `bin/fern -O` at 671c1109a.
- C: libdeflate 1.24, the raw DEFLATE API, built with CMake `Release` and the
  harness with `cc -O2` (Apple clang 17).
- Input: the 12 Silesia files, 211.9 MB in total. Each file is in memory
  before the clock starts.
- Each time is the median of 5 runs. Each slowdown below is the median over
  the 12 files.
- Each decoder also decodes the other encoder's stream. Every decode is
  compared with the original file, and all of them match.

std/deflate has one compression setting. For each file, the comparison uses
the libdeflate level whose compressed size is nearest to Fern's, so the two
do comparable work. Over the corpus, Fern's ratio is 3.085. That is between
libdeflate level 4 (3.051) and level 5 (3.109).

## Results

| File | MB | Fern ratio | Nearest level | Fern compress MB/s | libdeflate MB/s | Slower | Fern decompress MB/s | libdeflate MB/s | Slower |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| dickens | 10.2 | 2.579 | 4 | 12.9 | 137.4 | 10.7x | 75.8 | 1075.2 | 14.2x |
| mozilla | 51.2 | 2.672 | 4 | 18.6 | 170.5 | 9.2x | 73.2 | 863.8 | 11.8x |
| mr | 10.0 | 2.697 | 2 | 18.3 | 228.5 | 12.5x | 84.3 | 1048.8 | 12.4x |
| nci | 33.6 | 10.418 | 5 | 37.4 | 336.5 | 9.0x | 212.5 | 3434.3 | 16.2x |
| ooffice | 6.2 | 1.983 | 5 | 15.9 | 103.1 | 6.5x | 53.9 | 696.4 | 12.9x |
| osdb | 10.1 | 2.696 | 4 | 27.4 | 182.6 | 6.7x | 81.8 | 1348.5 | 16.5x |
| reymont | 6.6 | 3.434 | 5 | 14.1 | 142.4 | 10.1x | 98.3 | 1365.3 | 13.9x |
| samba | 21.6 | 3.953 | 5 | 23.0 | 191.0 | 8.3x | 109.8 | 1481.0 | 13.5x |
| sao | 7.3 | 1.352 | 5 | 15.6 | 92.6 | 5.9x | 40.8 | 627.1 | 15.4x |
| webster | 41.5 | 3.344 | 5 | 17.7 | 138.2 | 7.8x | 94.8 | 1175.6 | 12.4x |
| x-ray | 8.5 | 1.421 | 8 | 19.0 | 103.0 | 5.4x | 43.9 | 578.6 | 13.2x |
| xml | 5.3 | 7.634 | 6 | 30.5 | 218.3 | 7.2x | 176.5 | 2825.2 | 16.0x |

- Compression: Fern is 8.05 times slower (median), from 5.4 to 12.5 times.
- Decompression: Fern is 13.69 times slower (median), from 11.8 to 16.5 times.
- When both decoders decode the same libdeflate level-6 stream, Fern is 13.64
  times slower (median). Thus the gap is in the decoder, not in the streams
  that Fern makes.

## What is not measured

- x86-64. This host cannot run the x86-64 baseline (AVX2), so a later report
  will add those numbers.
- Streaming. Both libraries are called once on a whole file.
- Memory use.

## Gates

`bench/deflate_encode.fern` and `bench/deflate_decode.fern` put the encoder
and the decoder in the instruction-count corpus that CI compares on every
pull request. On this host, a 64 KiB encode retires about 98 million
instructions, which is about 1,500 instructions for each input byte.

### Integrated CI baseline

[Perf run 40010926319](https://github.com/JakeChampion/lang/actions/runs/40010926319)
measured commit `49c290377dfe6a930d3939efa99126c32be79880`, after the main merge.
The reports in `ci-perf-x86_64.txt` and `ci-perf-aarch64.txt` retain the exact
instruction and emitted-text counts. The deflate baseline uses those
measurements. The strict comparator passes for both reports.

| Architecture | Encode instructions | Decode instructions |
| --- | ---: | ---: |
| x86-64 | 95,861,038 | 237,885,657 |
| aarch64 | 102,600,617 | 226,739,308 |

These counts cover the CI benchmark inputs, separately from the Silesia wall
time comparison above.

`ci-selfhost-size.txt` retains the same run's exact emitted-size counts for
the two deflate programs on all three targets.
Their baseline rows use those measurements too.
