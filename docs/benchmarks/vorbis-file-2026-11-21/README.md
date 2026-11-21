# Indexed Vorbis file playback on Linux

Seven trials per file measured opening an immutable file, indexing it, decoding through EOF and hashing all interleaved PCM. Each trial started a fresh process. Both decoders consumed the same input and alternated execution order. Wall time includes the open and close. Peak RSS covers the entire process, including the C runtime and shared libraries. Fern also reports allocator events and fresh heap bytes.

| File | Fern median | libvorbisfile median | Fern peak RSS (median) | libvorbisfile peak RSS (median) |
| --- | ---: | ---: | ---: | ---: |
| Mono | 2.54 ms | 0.81 ms | 840 KiB | 1,220 KiB |
| Stereo | 11.10 ms | 1.00 ms | 1,372 KiB | 1,232 KiB |
| Eight channels | 7.15 ms | 1.05 ms | 1,000 KiB | 1,400 KiB |

All 35 Fern cases matched the frozen frame and sample counts. All 28 matched libvorbisfile cases reproduced the checksum of their frozen PCM. The remaining seven exercise explicit seeking or stream-only callbacks in the oracle and have Fern-only timings. The Reader correctness suite separately compares every output sample against the independent reference within 1e-6. [All x86-64 trials](x86-64.json)

To reproduce on native ARM64 or x86-64 Linux with the public headers and Xiph libvorbisfile 1.3.7 installed, run from the repository root after building the compiler.

```sh
cc -O2 scripts/audio-reference/vorbis-file.c -lvorbisfile -o build/vorbis-reference
python3 scripts/bench-vorbis-file --reference build/vorbis-reference > build/vorbis-file.json
```

## Nine-bit Huffman prefix table

The revised setup walks each Huffman tree down to depth nine once. A short code fills all table indices sharing its low bits. A longer code stores the continuation node. The previous setup walked from the root for each of the 512 table indices.

A native x86-64 comparison used the same optimized compiler and playback driver for both revisions, pinned to one CPU, with eleven pairs of fresh processes per fixture in alternating order. All 35 files had identical PCM checksums across the two builds. Allocations fell on every file, from 150,288 to 137,940 for one playback of each file. The sum of the per-file median wall times fell from 115.9 ms to 108.1 ms, with 28 files faster and seven slower. The corpus contains short streams, so these measurements include a larger share of setup time than long-running playback. [All 770 process measurements](prefix-x86-64.json.gz)

## Reference playback for seek fixtures

The seven seek fixtures now have reference timings as well as Fern timings. For six files, libvorbisfile opens the seekable input and calls `ov_pcm_seek(0)` before reading. For the interleaved file, it opens with stream-only callbacks. These modes reproduce the PCM used to generate the fixtures. They also affect the timed workload, so each result records the reference mode.

On x86-64 Linux, all 245 libvorbisfile trials across 35 files reproduced the frozen PCM checksum. All 245 Fern trials produced the expected frame and sample counts. The benchmark now fails if a reference trial disagrees with its frozen checksum. The separate Reader suite checks Fern's individual float samples with a tolerance of 1e-6. [Complete measurements](all-reference-modes-x86-64.json.gz)

## Verifying the timed Fern decoder

Before timing each fixture, the benchmark runs the same Fern executable once with a PCM output file. It compares every float against the frozen libvorbisfile samples with an absolute tolerance of 1e-6, rejects nonfinite differences, and records the largest error. Each timed Fern trial must reproduce the checksum of that verified output. PCM capture runs separately so writing the file does not enter the timing or memory measurements.

Across all 35 fixtures, native x86-64 Linux and the ARM64 Android executable under QEMU each reproduced 254,516 samples with a maximum absolute error of 1.79e-7. Three native timing trials per fixture reproduced the verified checksums. The empty logical stream produced zero samples.

## Verified playback on native x86-64 and ARM64

At commit `a3f910764d`, each native Linux runner played the same 35 indexed Ogg files. Fern and libvorbisfile 1.3.7 each decoded 254,516 interleaved samples per corpus pass. Untimed captures on each host were compared sample by sample with the frozen PCM. Fern's largest absolute error was 1.79e-7 on both hosts. The reference matched the frozen bits on x86-64 and its largest absolute error on ARM64 was 1.49e-7. Every timed playback reproduced its own verified checksum. [Workflow run](https://github.com/JakeChampion/lang/actions/runs/44944439665)

Each row gives the median of seven fresh processes per decoder. Time includes opening, indexing, decoding and closing the file. RSS is the peak for the Fern process. The full reports also record fresh heap bytes and reference RSS.

| Native Linux host | File | Fern (ms) | libvorbisfile (ms) | Fern allocations | Fern peak RSS (KiB) |
| --- | --- | ---: | ---: | ---: | ---: |
| x86-64 | Mono | 1.895 | 0.499 | 3,700 | 852 |
| x86-64 | Stereo | 6.979 | 0.631 | 4,985 | 1,384 |
| x86-64 | Eight channels | 5.406 | 0.967 | 11,653 | 1,008 |
| ARM64 | Mono | 2.047 | 0.530 | 3,700 | 884 |
| ARM64 | Stereo | 7.051 | 0.720 | 4,985 | 1,424 |
| ARM64 | Eight channels | 6.165 | 1.026 | 11,653 | 1,040 |

Each report contains 245 Fern trials and 245 reference trials, including the seven seek fixtures. [x86-64 measurements](verified-x86-64.json.gz) · [ARM64 measurements](verified-arm64.json.gz)
