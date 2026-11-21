# WavPack allocation profiles

Six independently decoded WavPack 5.8.1 files exercise both readers. Each contains 8,192 frames. Seven alternating timed trials use separate executables from the allocation profiles. Every instrumented execution matched the timed sample count and full PCM checksum. The producer also compared every Fern PCM byte with the independent reference before timing.

The source measurement opens the file and decodes through EOF. The warm measurement makes eight complete passes over already indexed input with a reusable output buffer after four warmups. Fern counts runtime allocation events. The C diagnostic executable interposes successful malloc, calloc and realloc calls from the executable and dynamically linked reference library on glibc. It snapshots the call count around the same measured operation, before printing or cleanup. These counters measure events at their respective allocator interfaces. Peak RSS, throughput and latency are available from the ordinary executables in the raw reports.

| Input | Source allocations, Fern / C | Eight warm passes, Fern / C | Source samples in bits.read / total |
| --- | ---: | ---: | ---: |
| music2 | 47,213 / 8 | 376,592 / 24 | 735 / 739 |
| music8 | 188,845 / 34 | 1,508,224 / 232 | 2,945 / 2,952 |
| noise2 | 53,542 / 8 | 427,160 / 24 | 833 / 838 |
| silence2 | 146 / 8 | 104 / 24 | 0 / 3 |
| integer32 | 70,035 / 8 | 559,024 / 24 | 1,092 / 1,095 |
| float2 | 63,641 / 8 | 507,976 / 24 | 992 / 995 |

The Fern profiles sample allocations over the whole process. On stereo music, 735 of 739 source samples included the WavPack bit reader and the runtime box allocator. Native disassembly of the bit reader showed a box allocation on its successful return path. Warm profiles that fill the profiler’s reservoir retain sample and drop counts in their headers. Sampling weights estimate bytes, and counts in the table count sampled events.

Both campaigns used native x86-64 Linux. The CPU and memory results are host measurements. [Source trials and profiles](source.json) · [Warm trials and profiles](warm.json) · [Previous warm CPU profile and comparison](../wavpack-decode-2026-11-09/README.md)

## Reproduce

```sh
scripts/bench-wavpack --compiler bin/fern-selfhost --allocation-profile --source --output /tmp/source.json
scripts/bench-wavpack --compiler bin/fern-selfhost --allocation-profile --rounds 8 --output /tmp/warm.json
```

The optional C allocation profile requires glibc. The production benchmark uses the regular system allocator.
