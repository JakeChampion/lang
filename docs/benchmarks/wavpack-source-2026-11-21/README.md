# WavPack sequential file playback

Seven paired trials per input compare Fern's bounded sequential Reader with WavPack 5.8.1 on one x86-64 Linux host. Both open the same warm-cache file, return the first 4,096 frames, and decode and checksum all 8,192 frames. The six inputs include music, silence, noise, full-width integers, float bits and eight-channel audio. Both readers produced the independent expected checksum in all 84 executions.

| Input | First PCM, Fern / C (ms) | Complete file, Fern / C (ms) | Fern allocation events | Peak RSS, Fern / C (KiB) |
| --- | ---: | ---: | ---: | ---: |
| music2 | 0.638 / 0.195 | 1.275 / 0.362 | 47,213 | 420 / 960 |
| music8 | 2.437 / 0.726 | 5.006 / 1.457 | 188,845 | 776 / 1148 |
| noise2 | 0.755 / 0.223 | 1.470 / 0.435 | 53,542 | 476 / 972 |
| silence2 | 0.109 / 0.058 | 0.215 / 0.107 | 146 | 380 / 948 |
| integer32 | 0.971 / 0.255 | 1.950 / 0.495 | 70,035 | 516 / 984 |
| float2 | 0.817 / 0.275 | 1.625 / 0.515 | 63,641 | 444 / 968 |

Medians include file open, decoder setup and reusable output allocation. Complete-file elapsed time includes the per-sample checksum and excludes startup, close and reporting. The first-group timer stops before checksumming. Each process reads one file. Trial order alternates and all processes use the same CPU. The raw report includes whole-process CPU time, fresh Fern heap bytes, sample counts, tool hashes and every trial. Peak RSS includes runtime and code. C allocation counts are unmeasured. [Raw results](results.json)

Eight WavPack suites passed 490 cases per target on native x86-64 Linux, ARM64 Linux and ARM64 Android under QEMU Linux user-mode, and WASI under Wasmtime. The same suites passed native sanitizers. All runs reported zero live bytes at exit. The byte-view, sequential and indexed suites each checked 132 frozen WavPack 5.8.1 references. The boundary suites cover malformed input, integrity, limits, short source reads and seeks.

Reproduce the file measurements with `scripts/bench-wavpack --source --compiler /path/to/fern --output results.json`. The existing warm byte-view benchmark remains available by omitting `--source`. Its [baseline and profile](../wavpack-decode-2026-11-09/README.md) cover repeated decoding after index construction. CPU and allocation improvements and physical Android latency, power and thermal measurements remain tracked in [#14779](https://github.com/JakeChampion/lang/issues/14779).
