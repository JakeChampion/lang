# Checked tile requests

The `pipeline/regions` module requests an owned rectangle from the current row graph.
`Region` contains zero-based `x` and `y` coordinates and positive `width` and `height` values.
The complete rectangle must fit inside the graph output.
The result has a tight stride and the graph output format.

Generated sources and materialized image sources support arbitrary rows.
Nearest nodes request one direct parent row. Bilinear nodes request at most two direct parent rows.
The `dependency_rows` query returns inclusive parent row indices after full graph validation.
Sources return `None` because they have no graph parent.
This access contract does not imply arbitrary access for future decoder sources.

Nonpositive dimensions return `Dimensions`. Invalid rectangles, node indices, and row indices return `Bounds`.
Invalid graph structure returns `InvalidGraph`.

Logical budget violations return `Budget`. `Pixel` wraps pixel storage and arithmetic errors.
Cooperative cancellation returns `Cancelled`.

## Planning and logical budgets

`plan` validates graph metadata, public source storage, region bounds, output dimensions, and output storage arithmetic.
It also checks total sample work and logical scratch before pixel allocation.
`render_tile` always repeats planning. A caller-provided `Plan` cannot bypass these checks.

The current providers still produce complete rows for a narrow crop.
For a tile with width W and height H, total samples equal H times the graph row estimate, plus W times H.
The extra term counts tile copy pixels.
This conservative estimate includes repeated parent evaluations without caching.

Logical scratch equals the graph row estimate plus twice the tile storage size.
The factor of two permits temporary copies during immutable tile updates.
The row estimate includes input rows and the returned output row.
Retained source images, graph metadata, callbacks, allocator overhead, and caller-held output tiles remain outside this estimate.
These budgets do not establish a process memory bound or recoverable allocation failure.

Bounds and work calculations use i64 intermediates. Pixel storage uses checked i32 arithmetic.
Every successful plan fits the configured limits before its values convert to i32.

## Execution and cancellation

The tile scheduler processes one output row at a time.
It copies the requested columns into the tile and releases that row after its final use.
The `render_tile` interface does not retain a row cache.

Cancellation checks occur before planning, before tile allocation, inside row execution, and before each group of 64 copy pixels.
A cancellation error releases input rows, partial output rows, and partial tile storage.
Cancellation remains cooperative and cannot interrupt a blocked callback.

## Verification

Eight cases cover padded RGB/RGBA crops, mixed filter chains, dependency rows, exact budgets, invalid descriptors, cancellation, and retained values.
A virtual ten-million-pixel graph returns a 240-byte tile near its bottom-right corner.
Its logical scratch estimate is 16,480 bytes and its sample estimate is 12,560.
Large-plan cases reject storage overflow and excessive recursive work before pixel allocation.

These cases execute natively with heap sanitization, on ARM64 under QEMU, and in Wasmtime.
They do not qualify process memory, codec access patterns, throughput, or production service behavior.

## Cached tile execution

`cached_plan` adds an explicit cache payload limit to the logical scratch estimate.
`render_cached_tile` repeats this validation before tile allocation and returns `CachedTile` with the image and row statistics.
Zero cache bytes disable storage. Negative cache bytes return `Budget`.
Total sample work retains the uncached estimate, including the tile copy pixels.
Cache hits do not increase the admitted work.

One `fold_rows` call processes the complete tile through a private per-call row cache.
The fold transfers the tile accumulator through each row consumer and returns its final value.
The consumer does not place the tile in a shared mutable cell.
The [row cache](row-cache.md) retains its payload, entry, eviction, cancellation, and ownership rules.

A four-column, eight-row RGBA tile from a 4x2 to 8x8 bilinear graph needs 464 logical scratch bytes with a 96-byte cache.
Its conservative work estimate is 160 samples. It reports 12 hits, ten rendered node rows, and four peak cache entries.
The scratch estimate includes 112 row bytes, twice the 128-byte tile storage, and 96 cache payload bytes.
These values exclude metadata, allocator overhead, source retention, and consumer-owned storage.

Six additional cases cover exact admission, RGB/RGBA crops across cache sizes, cancellation, tile isolation, and generic fold state with heap errors.
The original uncached row and tile interfaces keep their existing contracts.
Fusion, decoder row providers, isolated workers, and aggregate memory qualification remain separate work under #13215.
