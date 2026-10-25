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
The compiler reports `fern 0a6db7d5686b-dirty` and includes the append and computed-scrutinee fixes.
The measured compiler and library sources stayed fixed during collection.
The dirty state includes documentation and test integration changes.

## Observations

| Mode | Operations | Allocations | Fresh demand, bytes |
| --- | ---: | ---: | ---: |
| Unique pixel updates | 16 | 145 | 20,704 |
| Unique pixel updates | 128 | 1,041 | 20,704 |
| Shared input updates | 16 | 156 | 41,232 |
| Shared input updates | 128 | 1,052 | 41,232 |
| Retained image versions | 16 | 448 | 328,984 |
| Retained image versions | 128 | 3,587 | 2,629,984 |
| Discarded output rows | 16 | 44,220 | 1,800 |
| Discarded output rows | 128 | 353,676 | 1,800 |
| Retained output rows | 16 | 44,367 | 6,176 |
| Retained output rows | 128 | 354,834 | 38,352 |
| Cancelled output rows | 16 | 18,988 | 1,544 |
| Cancelled output rows | 128 | 151,820 | 1,544 |

The shared-input path costs more fresh storage than the unique-input path in this probe.
Retained image versions and retained rows increase fresh demand as the operation count increases.
Discarded and cancelled rows reuse allocator storage across these two request counts.
These observations do not prove a bound for other dimensions, graphs, input codecs, or longer executions.

The row path still performs many allocations per request.
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
