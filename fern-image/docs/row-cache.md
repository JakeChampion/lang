# Row batches and cache limits

`render_rows` delivers consecutive owned rows to a synchronous consumer callback.
The first row index and positive row count must fit inside the graph output.
The callback returns `Result[void, E]`, so callers retain their own error type.

`RowFailure` distinguishes a graph error from a consumer error.
Either error stops execution and releases private cache state.
Consumers can retain rows outside the call. Those rows remain outside the cache budget.

Cache state exists only during one batch call. Callers cannot supply or share that state.
Keys contain the node index and row index within the validated graph.
The uncached `render_row` interface keeps its existing behavior.

`fold_rows` transfers an explicit accumulator through each consumer and returns `RowFold` with its final value and statistics.
Its generic consumer accepts the accumulator and row, then returns the next accumulator or a consumer error.
`render_rows` uses this same engine with a consumer that does not return stored state.
The [cached tile interface](regions.md) uses a fold to fill its checked output rectangle.

## Capacity and admission

The cache byte limit counts row payload storage. Zero disables cache storage.
A row that cannot fit executes without cache storage.
Each node retains at most two rows, and the complete cache contains at most twice the graph node count.
The absolute entry ceiling is 128 because graphs contain at most 64 nodes.

The eviction policy removes the oldest row for a full node.
It then removes the oldest global entries until the new row fits the payload and entry limits.
Cache hits do not change entry order.

The logical scratch estimate adds the complete cache byte limit to row scratch.
The sample budget charges the uncached row estimate times the requested row count.
Cache hits do not admit additional work beyond that conservative budget.
Validation completes before row pixel allocation or consumer callbacks.

These limits exclude cache metadata, allocator overhead, retained source images, and caller-held outputs.
They do not establish a process memory bound, recoverable OOM, or an elapsed CPU deadline.
Consumer callbacks remain responsible for their own work and storage.

## Statistics and verification

`RowStats` reports delivered rows, hits, misses, peak cached payload bytes, and peak cache entries.
A miss renders that node row. A hit reuses its immutable row storage.
Cancellation checks occur before every node request, including hits, and during normal pixel loops.

Six cases verify exact output, admission, deterministic eviction, consumer errors, cancellation, and retained rows.
They include a 64-node graph with 128 cache entries and cancellation of a partial row after cached inputs.
Consumer-error cases retain rows after failure and propagate both heap strings and integer errors.
The image CI workflow runs native checks and a separate Wasm job with the pinned toolchain.
The Wasm sanitizer checks final live bytes. Its runtime does not provide the native use-after-free detectors.

## Allocator observations

The synthetic workload renders 16 or 128 rows from a virtual ten-million-pixel source.
Each output row contains 2,000 bytes. The consumer discards each row after checks.
Three fresh processes repeat each configuration with the same compiler and source hashes.
The qualifier enables the leak census and keeps allocator reuse active.

| Cache payload limit | Rows | Hits | Rendered node rows | Allocations | Fresh allocator demand, bytes |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 0 | 16 | 0 | 48 | 680,900 | 11,464 |
| 0 | 128 | 0 | 384 | 5,447,060 | 11,464 |
| 12,000 | 16 | 22 | 26 | 482,828 | 20,312 |
| 12,000 | 128 | 190 | 194 | 3,736,316 | 20,312 |

The enabled cache reaches four entries and 12,000 payload bytes in this workload.
It reduces repeated parent work and allocation counts, while fresh allocator demand increases.
These observations do not measure throughput, peak live bytes, peak RSS, isolated allocator overhead, or production behavior.

Raw observations are in [row-cache-m4.json](../benchmarks/results/row-cache-m4.json).
The collector checks source and tool hashes before and after every collection.

```sh
FERN_LEAKCHECK=1 bin/fern -target x86-64-linux \
  -o /tmp/image-row-cache fern-image/benchmarks/row_cache.fern
python fern-image/benchmarks/measure_row_cache.py \
  /tmp/image-row-cache "$PWD/bin/fern" /tmp/row-cache.json
```

The earlier allocation record describes its own compiler and source hashes.
It does not describe the new cache implementation.
Decoder providers, fusion, aggregate memory, and worker isolation remain separate work.
