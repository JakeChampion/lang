# Native allocator observations

These probes measure allocation counts and fresh allocator demand for small, single-process workloads.
They do not measure peak live storage or peak RSS.
They do not establish a process memory bound, recoverable OOM behavior, or production safety.

## Workloads and method

The pixel source is a 64x64 RGBA image with 16,384 storage bytes.
The unique update path releases its input reference after the update call.
The shared path reads the original image after that call.
The retained path keeps every intermediate image and checks its preserved bytes.

The row source represents a virtual 64x10000 RGBA image.
A bilinear node produces dimensions of 32x20000 and returns 128 storage bytes per row.
Separate modes discard rows, retain rows, or cancel after input rows and output storage exist.
The cancellation mode checks an explicit cancellation error on every request.

The eager modes repeatedly resize a transparent 64x64 RGBA source to 32x32 with bilinear interpolation.
Each output contains 4,096 bytes. Separate modes discard or retain those outputs.
The transparent input does not qualify representative color-processing throughput.

Each mode executes 16 or 128 operations in a fresh process.
Three processes repeat each configuration. All three produce identical counters for that configuration.
The probe checks output bytes and alias preservation before it returns success.
The collector requires a zero exit status and zero final live bytes.

The compiler uses `FERN_LEAKCHECK=1` without the sanitizer quarantine, so the allocator can recycle released storage.
`__heap_bump_bytes()` reports bytes that the allocator obtained fresh. This value includes allocation granularity.
It does not report the maximum live payload or resident pages.
`__heap_alloc_count()` counts workload allocations for input, graph, callback, and temporary objects.
Both counters stop before report construction.

Raw results include compiler, binary, and source hashes in [memory-m4.json](../benchmarks/results/memory-m4.json).
The compiler reports `fern 8f1d021a557a`, which is main on 2026-11-27.
The measured compiler and library sources stayed fixed during collection.
The collector rejects changes to measured sources, tools, or its own script during collection.

## Observations

| Mode | Operations | Allocations | Fresh demand, bytes |
| --- | ---: | ---: | ---: |
| Unique pixel updates | 16 | 4 | 20,592 |
| Unique pixel updates | 128 | 4 | 20,592 |
| Shared input updates | 16 | 7 | 41,096 |
| Shared input updates | 128 | 7 | 41,096 |
| Retained image versions | 16 | 67 | 328,488 |
| Retained image versions | 128 | 518 | 2,626,800 |
| Discarded output rows | 16 | 7,502 | 1,616 |
| Discarded output rows | 128 | 59,918 | 1,616 |
| Retained output rows | 16 | 7,521 | 5,272 |
| Retained output rows | 128 | 60,052 | 32,072 |
| Cancelled output rows | 16 | 2,366 | 1,464 |
| Cancelled output rows | 128 | 18,830 | 1,464 |
| Discarded eager outputs | 16 | 114,771 | 25,968 |
| Discarded eager outputs | 128 | 918,147 | 25,968 |
| Retained eager outputs | 16 | 114,790 | 104,144 |
| Retained eager outputs | 128 | 918,281 | 687,512 |

The unique and shared update paths make a constant number of allocations at both request counts.
The unique path writes each update into the storage of its input.
The shared path copies the input one time, and then it writes the copy in place.
Thus the shared-input path costs more fresh storage than the unique-input path in this probe.

Retained image versions and retained rows increase fresh demand as the operation count increases.
Discarded eager outputs, discarded rows, and cancelled rows reuse allocator storage across these two request counts.
These observations do not prove a bound for other dimensions, graphs, input codecs, or longer executions.

The row path still performs about 470 allocations per request, and the eager path about 7,200.
The allocation count does not measure bytes copied or elapsed CPU time.
Fresh demand includes capacity and released slots that the allocator does not yet reuse.
The difference from logical payload size is not an isolated allocator-overhead measurement.

## Reproduction

Install the pinned toolchain with `scripts/toolchain-env` from the repository root.
Build a compiler that includes the append fix and the computed-scrutinee fix.

```sh
make build
FERN_LEAKCHECK=1 bin/fern -target x86-64-linux \
  -o /tmp/image-memory-probe fern-image/benchmarks/memory_probe.fern
python fern-image/benchmarks/measure_memory.py \
  /tmp/image-memory-probe "$PWD/bin/fern" /tmp/memory-m4.json
```

The regular image test script executes the same workload modes with the native heap sanitizer.
That run checks cleanup and alias behavior. Its quarantine prevents allocator reuse, so its bump values are not qualification samples.

## Remaining qualification

PNG input, compressed streams, filtered bytes, complete outputs, and codec scratch still need aggregate measurements.
Peak RSS, allocation failures, isolated workers, memory pressure, CPU deadlines, and service concurrency remain unqualified.
Issue #13222 stays open until its acceptance criteria and CI pass.
