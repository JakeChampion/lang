# Cached nearest-row fusion

Issue #13374 extends nearest-chain fusion to an explicit cached batch API.
`fold_rows_fused` accepts the same arguments and returns the same result types as `fold_rows`.
Existing `fold_rows` and `render_rows` behavior and statistics remain unchanged.
The cached tile interface still uses the existing fold without fusion.

## Execution contract

The API uses one private FIFO cache for each batch.
It preserves the payload limit, the maximum of two entries per node, and the global entry limit.
Eligible nearest chains request their root through that cache.
The output also enters the cache through the normal row request path.
The fused chain does not materialize or cache its intermediate nearest rows.

Coordinate composition preserves every integer nearest step in both dimensions.
Bilinear nodes stop a chain and retain their interpolation and byte rounding.
Fusion still requires multiple nearest nodes and no more horizontal coordinate evaluations than the conservative unfused plan.
An expanding chain can retain ordinary per-node execution when this condition fails.

Graph validation completes before execution, including nodes that fusion skips.
The existing scratch estimate and cumulative sample admission remain unchanged.
The complete cache allowance remains part of logical scratch admission, even when fewer payload bytes enter the cache.
These logical charges exclude allocator slack, metadata, source retention, and caller-held values.
They do not establish physical scratch or process memory bounds.

`RowStats.rows` counts successful consumer calls.
Hits and misses count actual cache lookups. Intermediate nodes that fusion skips cause no lookup or miss.
Cache peaks count retained row payload and entry count rather than allocator bytes or RSS.
A zero-byte cache retains these lookup statistics without stored row payload.

The consumer can retain or modify its owned row values without changing source values or cached rows.
Consumer errors and cooperative cancellation release private cache state and partial outputs.
Callbacks can still have external effects that an error does not reverse.

The fused API can bypass an intermediate row that the existing API can reuse.
The measured workload below does not establish an improvement for every cache pattern or graph.
The API provides no timing, hard-deadline, performance-parity, or production-safety guarantee.

## Checks and observations

Eight targeted cases cover independent RGB/RGBA coordinates, cache hits, mixed filters, exact admission, and upsample fallback.
They also cover consumer errors, retained values, every cancellation poll, the 64-node limit, and invalid skipped nodes.
The existing six fusion cases retain their original cache statistics.

One binary selects exactly one existing or fused fold in each fresh process.
Both paths use identical graph, consumer, compiler, and stdlib sources.
The workload has a virtual solid RGBA source of 1024 by 8192 pixels.
Four nearest nodes produce widths 768, 512, 256, and 128.
Their heights are 10000, 16000, 20000, and 24000.
The consumer checks endpoint bytes and discards each row.

The collector runs both modes at 8 and 32 rows, with cache limits of zero and 12000 bytes.
Three repeats produce 24 fresh processes without warmups.
The leak census operates without sanitizer quarantine for these allocator observations.
The common logical scratch estimate remains 21504 bytes, and the sample estimate remains 2688 per output row.

At 32 rows with a 12000-byte cache, rendered node rows decrease from 109 to 44.
Allocation counts decrease from 726033 to 185832. Fresh allocator demand decreases from 30080 to 19232 bytes.
Cache payload peaks decrease from 11776 to 9216 bytes.
All repeats reproduce these counters and finish with zero live bytes.

The raw record includes exact compiler, executable, collector, package, Python, and workspace stdlib hashes.
The collector checks source and tool immutability before and after the observations.
Fresh allocator demand does not measure peak live bytes, isolated allocator overhead, RSS, or a process bound.
The solid fixture does not qualify representative color throughput.
These observations provide work and allocation evidence without a wall-time measurement.

## Reproduce

Run these commands from the repository root with a compatible local compiler.

```sh
FERN_LEAKCHECK=1 ./bin/fern -target x86-64-linux \
  -o /tmp/cached-fusion fern-image/benchmarks/cached_fusion.fern
python3 fern-image/benchmarks/measure_cached_fusion.py \
  /tmp/cached-fusion "$PWD/bin/fern" /tmp/cached-fusion.json
```

The raw record resides in `benchmarks/results/cached-fusion-m4.json`.
