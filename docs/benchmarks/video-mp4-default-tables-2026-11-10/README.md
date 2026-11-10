# MP4 default-table memory

The native MP4 packet indexer previously expanded absent composition and sync tables into per-sample arrays.
The arrays contained zero offsets and true sync flags, respectively.
The indexer now applies these defaults directly to each packet.
It retains the existing validators for explicit tables.
The public timing readers still return one value per sample.
An explicit empty sync table still marks every packet as a non-sync sample.

## Matched memory measurements

The base source is `2fa4d82b27c43eb9cc34c062d307218111d2db44`.
Both builds use one nonembedded compiler from that base, with explicit stdlib paths.
Its SHA256 is `26418c31efc483a6234e425b42475804134a0b1d7dddbfb3218ba88d2101aeaf`.
No compiler changes are part of this comparison.

| Packets | Composition table | Sync table | Before peak RSS | After peak RSS | Difference |
| ---: | --- | --- | ---: | ---: | ---: |
| 65,536 | Absent | Absent | 12.625 MiB | 11.625 MiB | -1 MiB |
| 65,536 | Absent | Present | 12.625 MiB | 12.125 MiB | -0.5 MiB |
| 65,536 | Present | Absent | 12.625 MiB | 12.125 MiB | -0.5 MiB |
| 65,536 | Present | Present | 12.625 MiB | 12.625 MiB | 0 |
| 262,144 | Absent | Absent | 50.25 MiB | 46.25 MiB | -4 MiB |
| 262,144 | Absent | Present | 50.25 MiB | 48.25 MiB | -2 MiB |
| 262,144 | Present | Absent | 50.25 MiB | 48.25 MiB | -2 MiB |
| 262,144 | Present | Present | 50.25 MiB | 50.25 MiB | 0 |

The table shows medians from seven alternating matched groups on x86-64 Linux.
Each absent table removes one heap allocation from the complete index call.
Both versions allocate the same number of packet records.
The report includes each allocation count and every resource sample.
No general elapsed-time, native ARM, player-throughput, or device-power claim follows from these short shared-host trials.

## Instruction tradeoff

Valgrind 3.24 Cachegrind supplies a separate x86-64 guest instruction comparison at 262,144 packets.
Each process checks the same packet count and checksum as the memory samples.

| Composition table | Sync table | Before instructions | After instructions | Change |
| --- | --- | ---: | ---: | ---: |
| Absent | Absent | 94,739,939 | 91,331,804 | -3.60% |
| Absent | Present | 95,005,383 | 96,315,962 | +1.38% |
| Present | Absent | 98,413,333 | 96,053,907 | -2.40% |
| Present | Present | 98,678,777 | 101,038,065 | +2.39% |

The packet loop trades two default checks for the removed arrays.
The explicit-table control therefore has an instruction cost, with unchanged memory use.
These results support the memory claim, rather than a general CPU improvement.
The counts include startup, file input, the index, and checksum work.
They are not native ARM retired-instruction measurements.
Raw values are in `instructions.json`.

The driver reuses `scripts/video-compare/sample.c` for resource measurements.
The small native sampler starts before the benchmark, so Python memory does not set its RSS floor.
Each sample includes process startup, file input, the complete index, and a checksum over every packet.
The same compiler, input bytes, and benchmark program serve both versions.
The report includes the compiler, source, module, template, and input hashes.

## Workload

The test generator constructs synthetic MP4 metadata from the checked-in AVC description.
The file has one track, one chunk, a fixed one-byte sample size, and a unit decode duration.
It adjusts movie and track clocks and durations to match the packet count.
An explicit composition table contains one run with offset two.
An explicit sync table contains only sample one.
The four table combinations separate the two default arrays from the explicit-table control.

These files measure metadata storage. Their one-byte payloads are not decodable AVC pictures.
The Python driver independently computes the packet count and checksum.
The checksum includes offsets, sizes, DTS, PTS, durations, descriptions, and sync flags.
Every process must match both values before the driver saves its report.
The separate interoperability tests below use valid encoded video.

## Reproduction

Create a control worktree at the base source before the comparison.
Use a nonembedded compiler, or verify its explicit stdlib override.

```sh
python3 docs/benchmarks/video-mp4-default-tables-2026-11-10/run.py \
  --compiler build/video/mp4-defaults/compiler \
  --before-stdlib /workspace/lang-mp4-default-control/internal/stdlib \
  --after-stdlib internal/stdlib \
  --output build/video/mp4-defaults/report.json \
  --instruction-output build/video/mp4-defaults/instructions.json
```

The instruction comparison requires Valgrind on PATH and its configured library directory.
Omit the instruction output option to repeat only the memory measurements.

## Correctness

Native sanitizer and WASI cases cover all four table combinations and preserve both public timing arrays.
They retain signed and unsigned composition offsets, negative and zero sample counts, and absent versus empty sync semantics.
Fresh no-B-frame and all-intra AVC streams exercise one and two absent tables, respectively.
FFprobe independently supplies their packet metadata.
FFmpeg verifies decoded pixels through native complete-view and real WASI file input.
The checked-in vectors need no FFmpeg installation during normal tests.
