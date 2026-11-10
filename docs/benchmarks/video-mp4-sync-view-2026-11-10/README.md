# MP4 sync-table source view

The native MP4 indexer previously expanded each explicit sync table into one temporary boolean per packet.
The indexer now validates the table once and reads its sample numbers through a scalar cursor.
It retains the original metadata bytes, without another sync array.
Public `sync_samples` still returns one boolean per sample through the same validator.

An absent table still marks every sample as sync.
An explicit empty table still marks no samples as sync.
Strict order and sample bounds prove that a table with one entry per sample marks every sample as sync.
This dense case uses a direct all-sync flag after validation.

## Matched memory

Both builds use compiler SHA256 `50d870aa8d548e67d2c48434fdcf9678d749ef3faf545f7a8dda2de0ab996999`.
The control source is main `529de0245476e2625571b180ced1ce4b476c6305`.
Its Fern compiler source is identical to the producer source at `6d8861c3d1`.
The compiler receives explicit before and after stdlib paths.
No compiler change or baseline change enters this comparison.

The native sampler records seven alternating matched groups for each workload on x86-64 Linux.
The table contains median peak process RSS, including startup, file input, packet metadata, and checksum work.

| Packets | Composition table | Sync table | Before RSS | After RSS | Difference |
| ---: | --- | --- | ---: | ---: | ---: |
| 65,536 | Absent | absent | 11.625 MiB | 11.625 MiB | +0 MiB |
| 65,536 | Absent | empty | 12.125 MiB | 11.625 MiB | -0.5 MiB |
| 65,536 | Absent | sparse | 12.125 MiB | 11.625 MiB | -0.5 MiB |
| 65,536 | Absent | dense | 12.625 MiB | 12.125 MiB | -0.5 MiB |
| 65,536 | Present | absent | 12.125 MiB | 12.125 MiB | +0 MiB |
| 65,536 | Present | empty | 12.625 MiB | 12.125 MiB | -0.5 MiB |
| 65,536 | Present | sparse | 12.625 MiB | 12.125 MiB | -0.5 MiB |
| 65,536 | Present | dense | 13.125 MiB | 12.625 MiB | -0.5 MiB |
| 262,144 | Absent | absent | 46.25 MiB | 46.25 MiB | +0 MiB |
| 262,144 | Absent | empty | 48.25 MiB | 46.25 MiB | -2 MiB |
| 262,144 | Absent | sparse | 48.25 MiB | 46.25 MiB | -2 MiB |
| 262,144 | Absent | dense | 50.5 MiB | 48.5 MiB | -2 MiB |
| 262,144 | Present | absent | 48.25 MiB | 48.25 MiB | +0 MiB |
| 262,144 | Present | empty | 50.25 MiB | 48.25 MiB | -2 MiB |
| 262,144 | Present | sparse | 50.25 MiB | 48.25 MiB | -2 MiB |
| 262,144 | Present | dense | 52.5 MiB | 50.5 MiB | -2 MiB |

Explicit tables remove 0.5 MiB at 65,536 packets and 2 MiB at 262,144 packets in these samples.
Absent-table memory remains unchanged.
At 262,144 packets, each explicit-table case removes 16 heap allocations from array growth.
Both versions retain the same packet records.
The report preserves every resource sample and allocation count.

## Instruction tradeoff

Actual Valgrind 3.24 Cachegrind results cover all eight table combinations at 262,144 packets.
The counts include startup, file input, the complete index, and the checksum.

| Composition table | Sync table | Before instructions | After instructions | Change |
| --- | --- | ---: | ---: | ---: |
| Absent | absent | 91,331,710 | 92,118,171 | +0.86% |
| Absent | empty | 96,315,803 | 95,002,669 | -1.36% |
| Absent | sparse | 96,316,026 | 95,003,045 | -1.36% |
| Absent | dense | 113,093,058 | 108,109,870 | -4.41% |
| Present | absent | 96,053,813 | 96,840,274 | +0.82% |
| Present | empty | 101,037,906 | 99,724,772 | -1.30% |
| Present | sparse | 101,038,129 | 99,725,148 | -1.30% |
| Present | dense | 117,815,161 | 112,831,973 | -4.23% |

Explicit-table instructions decrease in this run, with a small instruction cost for absent-table controls.
These are x86-64 guest instructions, not native ARM retired instructions.
Short elapsed samples include process startup and shared-host noise.
The results do not establish a general time improvement, player throughput, physical power, or lip-sync accuracy.

## Workload and correctness

The driver reuses the previous MP4 metadata generator and the native sampler in `scripts/video-compare/sample.c`.
The files contain 65,536 or 262,144 one-byte packets and unit decode durations.
The payloads are not decodable AVC pictures.
Real FFmpeg interoperability uses separate valid encoded files.

Composition tables are absent or contain one explicit run.
Sync tables are absent, explicit empty, sparse with three entries, or dense with one entry per sample.
The media bytes precede the movie metadata, so added sync entries do not move packet offsets.
An independent Python calculation supplies the packet count and checksum over every retained packet field.
Every before and after process must match both values.

`report.json` contains all 16 memory workloads.
`instructions.json` contains all eight instruction controls.
`report.json` preserves compiler, source, module, and input hashes.
`instructions.json` preserves compiler, source, and input hashes.

## Reproduction

Create the control checkout at main `529de0245476e2625571b180ced1ce4b476c6305`.
Use the same nonembedded compiler for both stdlib trees.

```sh
python3 docs/benchmarks/video-mp4-sync-view-2026-11-10/run.py \
  --compiler build/video/mp4-sync/compiler \
  --before-stdlib /workspace/lang-mp4-sync-control/internal/stdlib \
  --after-stdlib internal/stdlib \
  --output build/video/mp4-sync/report.json \
  --instruction-output build/video/mp4-sync/instructions.json
```
