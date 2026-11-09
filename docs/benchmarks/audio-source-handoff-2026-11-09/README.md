# Audio source handoff: capacity defeats unconditional transfer

The default `std/audio/source` implementation retains its compact-copy path.
This experiment checks whether an empty window can take ownership of a callback's byte array without a copy.
Direct transfer removes allocation work but can retain storage far beyond the returned length.
Issue #14876 tracks a bounded-capacity operation before adoption.
Issue #14871 tracks the broader source CPU and allocation costs.

The source callback contract limits the returned length.
It does not limit allocation capacity.
A private array can retain its original allocation after `truncate(1)`.
The receiver cannot infer retained storage from `len()` alone.

## Matched ordinary-input campaign

The baseline uses the CAF source implementation from #14870.
The candidate adds the four-line empty-window transfer in `rejected-transfer.patch`.
The candidate source commit is `a5abdb51a1`.
Both use the self-built fixed-point compiler with SHA-256 `a9a739de6bd908526e0820db913c028812ba5da58e4fb54ac9f65d5724814144`.

The campaign reuses the frozen CAF inputs and benchmark driver from `../caf-source-2026-11-09/`.
Seven trials reverse execution order on alternate trials.
Each variant confirms packet counts, total frames, and the first independent packet checksum.
The source reader and byte-view reader remain in one executable.

| Packets | Source API | Allocation events, prior / transfer | Median paired time ratio |
| ---: | --- | ---: | ---: |
| 1,000 | Reader | 23,263 / 22,253 | 0.9797 |
| 1,000 | Retained callbacks | 15,189 / 14,179 | 0.9699 |
| 10,000 | Reader | 230,290 / 220,279 | 1.0225 |
| 10,000 | Retained callbacks | 150,210 / 140,199 | 0.9463 |
| 100,000 | Reader | 2,300,464 / 2,200,442 | 0.9845 |
| 100,000 | Retained callbacks | 1,500,318 / 1,400,296 | 0.9438 |

Direct transfer removes about one allocation event per packet in these files.
At 100,000 packets, the reduction is 4.3% for Reader and 6.7% for retained callbacks.
Fresh allocator bytes and peak RSS remain nearly unchanged for these ordinary chunks.

The unchanged byte-view path reports 2.7% to 5.6% more time in the same combined executable.
This control limits attribution of the small source-time differences.
The report makes no isolated CPU-speed claim from this experiment.
`paired-results.json` retains every trial and executable hash.

## Oversized-capacity witness

The callback allocates a private 8 MiB byte array and returns `truncate(1)`.
The caller retains sixteen source windows, each with a one-byte limit.
The probe checks every retained byte after the memory read.
Both implementations preserve exact values and free every allocation at exit.

| Implementation | Fresh allocator bytes | Peak process RSS |
| --- | ---: | ---: |
| Compact copy | 83,889,328 | 65,644 KiB |
| Unconditional transfer | 167,775,528 | 131,208 KiB |

Direct transfer almost doubles peak RSS in this witness.
It retains the large callback allocations in the one-byte windows.
The original implementation copies the valid prefix and releases each oversized callback result.
Fresh allocator bytes are cumulative new storage, not peak live heap.
Peak RSS includes allocator retention and code as well as the live source windows.

`capacity-probe.fern` and `capacity-results.json` retain the witness.
The final branch restores the compact-copy implementation.
It adds an alias control for shared callback chunks and later buffer replacement.
Native sanitizer and leak checks pass that control with 68 balanced allocations and zero live bytes.

## Required follow-up

A bounded handoff must inspect allocation capacity or compact oversized storage before retention.
It must also release excess storage from zero-length callback results.
The compiler and runtime must own physical-layout knowledge.
The audio library must preserve its callback and lifetime contracts.
Issue #14876 records these requirements and cross-target qualification.
