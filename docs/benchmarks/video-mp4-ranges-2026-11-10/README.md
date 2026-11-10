# MP4 media-range search

The complete-view and seekable-file indexers supply ordered, disjoint media payloads.
The previous sample-table reader scanned these payloads for each packet.
The default video limits permit 1,048,576 packets and 65,536 boxes.
Independent packet and box limits did not prevent their product from controlling search work.

The new reader checks range order once, without another array.
More than 16 ordered, disjoint ranges use a binary search for each packet.
Small, unordered, and overlapping range lists retain a linear search.
The caller can supply chunk offsets in any order.
Packets must still fit within one payload, without an overflow in the end calculation.

The 16-range cutoff follows a measured comparison.
Binary searches with eight and 16 ranges increased instruction counts in an initial trial.
The final measurements below retain linear searches for those sizes.
This change preserves direct-call admission for unordered, overlapping, empty, and invalid ranges.

## Matched measurements

The base source is `e4494347c2f16a96727563c4495bd9826fdfadd0`.
The same nonembedded compiler builds both versions with explicit stdlib paths.
Its SHA256 is `68e415467a0d3b40323959c689318c918d9a2adc8415d58e834dde17de914a83`.
The compiler source matches that base. No compiler changes are part of this comparison.

Each workload constructs 4,096 one-packet chunks and indexes them ten times.
The last-range workload addresses only the final payload.
The scattered workload selects payloads with `(packet_index * 73) % range_count`.
The checksum includes every packet offset and decode timestamp.
The Python driver independently computes the expected checksum.

| Ranges | Chunk locations | Before instructions | After instructions | Change |
| --- | --- | ---: | ---: | ---: |
| 1 | Last | 8,254,312 | 8,377,316 | +1.49% |
| 8 | Scattered | 10,024,621 | 10,147,625 | +1.23% |
| 16 | Scattered | 11,991,588 | 12,114,592 | +1.03% |
| 32 | Scattered | 15,925,375 | 14,938,809 | -6.19% |
| 128 | Scattered | 39,527,689 | 17,064,643 | -56.83% |
| 2,048 | Last | 1,014,591,478 | 21,680,432 | -97.86% |
| 2,048 | Scattered | 511,569,976 | 21,730,170 | -95.75% |

Valgrind 3.24 Cachegrind measures retired guest instructions on x86-64 Linux.
These counts include process startup, table construction, packet output, and checksum work.
They are not native ARM measurements or whole-player throughput.
The small-range instruction increases remain visible in the table.

Seven alternating matched groups supply the elapsed and memory samples in `report.json`.
Shared-host contention and process startup affect these short elapsed samples.
No general elapsed-time claim follows from this report.
Both versions report 41,072 allocations inside the index loop for every workload.
The native process sampler reports a median peak RSS of 512 KiB for both versions.
These unchanged coarse RSS samples do not establish an improvement in memory use.

## Reproduction

Create a control worktree at the base source before the comparison.
Use a nonembedded compiler, or verify that explicit stdlib paths override its embedded sources.
Put Valgrind on PATH and set VALGRIND_LIB if the installation requires it.

```sh
python3 docs/benchmarks/video-mp4-ranges-2026-11-10/run.py \
  --compiler build/video/mp4-ranges/compiler \
  --before-stdlib /workspace/lang-mp4-range-control/internal/stdlib \
  --after-stdlib internal/stdlib \
  --output build/video/mp4-ranges/report.json
```

The driver reuses `scripts/video-compare/sample.c` for process resource measurements.
It builds both programs with one compiler and checks every checksum before it saves a report.
The report contains all elapsed samples, instruction counts, allocations, memory samples, and source hashes.

## Correctness

Native sanitizer and WASI tests compare admission with an independent linear reference.
Cases cover both search paths, gaps, adjacent payloads, empty payloads, overlaps, negative inputs, and 64-bit end boundaries.
A separate test preserves packet times and descriptions across backward-moving chunks.

Two additional AVC and HEVC fixtures contain 77 media-data boxes, including empty payloads.
Their six packets occupy separate payloads in reverse physical order.
The existing FFprobe and FFmpeg oracle checks packet metadata and decoded pixels through native and real WASI file input.
Normal tests use checked-in references and require no FFmpeg installation.

`cutoff-scan.json` records the earlier four-range cutoff with the same compiler.
It is cutoff evidence, not final-source qualification. Its source hash differs from the final report.
