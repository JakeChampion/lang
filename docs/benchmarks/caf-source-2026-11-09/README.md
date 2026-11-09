# Bounded CAF ALAC file playback

`std/audio/caf/file` indexes a borrowed seekable source and decodes one selected ALAC packet at a time.
It retains no complete encoded file or complete PCM.
The byte-view and source readers share metadata, layout, packet-header, and presentation checks.
The cursor preserves exact integer samples, trim ranges, sample-accurate seeks, and caller-owned output buffers.

Each cooperative step performs at most one seek, one bounded read, or one packet inspection or decode.
Unknown chunk payloads require no reads.
Chunk extents and packet offsets use signed 64-bit counts.
The caller supplies the same immutable source and closes it after success or failure.

Issue #14866 and PR #14870 track the source API under #14685 and #14508.
PR #14869 fixes the compiler dependency defect that the sparse-source callback exposed.
Issue #14871 tracks the measured index CPU and allocation costs below.

## Retained storage and limits

The index stores offsets, packet sizes, sample positions, configuration, and format information.
Its arrays use 20 logical bytes per packet and an eight-byte terminal position.
Native scalar slots increase that payload to 24 bytes per packet before capacity and allocator overhead.

The default packet window holds at most 1 MiB.
The packet-table window normally holds 16 KiB.
Both windows can coexist during index construction.
The separate file budget covers the complete source, while the audio input budget applies to one packet.
The packet count and frame capacity also have explicit limits.

## Matched index measurements

The campaign uses one native AMD EPYC 9V74 Linux x86-64 host.
Both APIs use the same self-built compiler and standard library.
The producer passes the stage-2/stage-3 fixed-point check.
Its SHA-256 is `a9a739de6bd908526e0820db913c028812ba5da58e4fb54ac9f65d5724814144`.
`results.json` retains source, producer, driver, executable, library, and input hashes.

The source snapshot for the benchmark is `099968a1ad`.
The generator repeats one independently checked ALAC packet in a CAF file with explicit variable packet sizes and durations.
Each packet contains 19 mono frames.
The three files contain 1,000, 10,000, and 100,000 packets.
Their sizes are 73,140, 730,140, and 7,300,140 bytes.

Five trials reverse API order on alternate trials after other local builds and tests finish.
The filesystem cache is warm.
Each row measures a new process and complete index construction from its input API.
The byte-view path includes the complete input read and retains those bytes through the RSS read.
The source path includes file open, chunk scans, packet-table reads, and packet-header inspections.

`view` uses `caf.inspect` over complete bytes.
`source` uses the cooperative `Builder.step(Reader)` method.
`source-with` retains seek and read callbacks once and passes them to `Builder.step_with`.
Every variant confirms the same packet count, total frames, and first decoded packet checksum.
The complete independent PCM campaign remains separate from this index measurement.

| Packets | API | Median index time | Allocation events | Fresh allocator bytes | Median peak RSS |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1,000 | Byte view | 0.136 ms | 1,044 | 225,208 | 384 KiB |
| 1,000 | Source / Reader | 0.671 ms | 23,263 | 67,536 | 240 KiB |
| 1,000 | Source / retained callbacks | 0.660 ms | 15,189 | 67,456 | 240 KiB |
| 10,000 | Byte view | 1.348 ms | 10,056 | 2,555,832 | 2,312 KiB |
| 10,000 | Source / Reader | 6.889 ms | 230,290 | 1,009,120 | 888 KiB |
| 10,000 | Source / retained callbacks | 6.685 ms | 150,210 | 1,009,040 | 888 KiB |
| 100,000 | Byte view | 12.941 ms | 100,065 | 22,544,312 | 19,988 KiB |
| 100,000 | Source / Reader | 67.195 ms | 2,300,464 | 7,889,888 | 5,736 KiB |
| 100,000 | Source / retained callbacks | 63.339 ms | 1,500,318 | 7,889,808 | 5,736 KiB |

At 100,000 packets, the source paths reduce peak process RSS by 71.3% and fresh allocator bytes by 65.0%.
Callback reuse reduces allocation events by 34.8% against the Reader convenience path.
The source paths still take about five times longer for index construction in this campaign.
The median paired source-to-view ratios range from 4.84 to 5.50 across the three files and two source variants.
These results establish a memory tradeoff and a CPU gap, not performance leadership.

Allocation events include both fresh allocation and allocator reuse.
Fresh allocator bytes are cumulative new storage and do not measure peak live heap.
RSS includes code, input, index arrays, allocator capacity, and small measurement buffers.
The report takes the RSS read before the first packet decode.
The measurements cover a warm filesystem cache and one repeated packet, not cold storage or complete-track decode.

## Profiles of the source path

The retained-callback variant supplies the profile workload.
Allocation mode inspects the 10,000-packet file once and retains 2,349 samples with no drops.
The systematic interval is one sample per 64 allocation events.
Weights estimate rounded allocated bytes and include allocator reuse.
Periodic workloads can bias systematic samples.

The profile attributes 17.3% of estimated bytes to `source.Buffer.ensure` and 16.4% to file-window construction.
The read callback accounts for 15.3%, and the builder step accounts for 13.6%.
Configuration reads account for 12.8%, and byte-view construction accounts for 8.2%.
These labels identify the nearest non-runtime caller in each stack.
They do not prove the cause of each allocation.

CPU mode repeats index construction of the 100,000-packet file 100 times.
It retains 648 samples with no drops.
Read and seek callbacks account for 193 and 102 samples at the nearest non-runtime frame.
The builder step accounts for 78 samples.
This profile identifies source I/O and state transitions as priorities for #14871.

The profile executable and its output are separate from the uninstrumented time campaign.
`alloc.folded`, `cpu.folded`, and `cpu-profile.fern` retain the evidence and repeated driver.

## Correctness and qualification

All 65 independent CAF cases pass through the source API across four corpus shards.
They cover all supported layouts, packet tables, partial packets, trim boundaries, and repeated seeks.
The byte-view corpus and boundary cases also pass after the metadata extraction.
The pinned independent ALAC PCM remains unchanged.

Thirteen suites pass with the final self-built producer, for 39 target builds and 26 executions.
Native x86-64 executions pass sanitizers, strict IR, and balanced leak checks with zero live bytes.
WASI executions and Android ARM64 shared-library builds pass.
The six source boundary cases cover incomplete prefixes, malformed metadata, limits, callback failures, invalid seeks, and undersized output.
They also compare exact independent PCM beyond 4 GiB without reads of the sparse gap.

Registered Reader tests pass on native x86-64 and WASI with complete, actual sparse, truncated, and missing files.
The actual sparse CAF file places audio beyond 4 GiB without a large physical allocation.
The test closes the Reader after success and failure.

Both interpreters pass the independent smoke and all six source boundary cases.
The boundary suite takes 4.8 seconds in Go and 34.3 seconds in the self-host interpreter.
Complexity limits and source lint pass.
`qualification.json` and the adjacent logs retain the results.

Native ARM64 execution, the full CI verdict, and physical Android playback measurements remain incomplete.
The report does not establish Android latency, power, thermal behavior, or underrun rates.

## Reproduction

```sh
scripts/bench-caf-source \
  --compiler /path/to/self-built/stage2 \
  --output /tmp/caf-source --trials 5
```

The benchmark compiles `bench/audio/caf_source.fern` with the current explicit standard-library path.
The generated files use the independent CAF smoke packet and validated packet tables.
The report retains every trial and all reproduction hashes.
