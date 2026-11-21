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
