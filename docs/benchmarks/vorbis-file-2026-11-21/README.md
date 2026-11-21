# Indexed Vorbis file playback on x86-64 Linux

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
