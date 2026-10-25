# Row pipeline and future region engine

The first pipeline slice renders individual RGB8/RGBA8 rows on demand.
It supports materialized image sources, generated solid sources, nearest-neighbor nodes, and bilinear nodes.
A solid source needs no complete image allocation.
Each nearest node requests one input row for the requested output row.
Each bilinear node requests at most two input rows.

The bilinear kernel shares the coordinate map and linear-light premultiplied-alpha interpolation with eager resize.
Successive nodes retain intermediate byte rounding, so their output matches successive eager operations.

`metadata` returns complete output dimensions. `render_row` returns an owned image with height one.
The graph's final node is its output.
Node inputs must refer to earlier nodes, which prevents cycles and forward references.
Public graph values receive full validation before execution.

## Logical resource limits

Caller limits cover pixels, row bytes, scratch bytes, nodes, and sample evaluations.
The graph contains at most 64 nodes. Row and scratch byte limits cannot exceed 64 MiB.
Dimensions and metadata products use i64 arithmetic before bounds checks.
Nearest coordinates share the checked integer center map with the eager resize module.

Source scratch is twice its row size. Nearest scratch adds twice the output row size to parent scratch.
Bilinear scratch also adds one parent row size, because the first row remains live during the second request.
The factor of two accounts for temporary copies during immutable updates.
The estimate excludes allocator overhead, graph metadata, retained source images, and caller-retained output rows.
It does not establish a hard process memory limit or a recoverable OOM guarantee.

The nearest sample budget adds the output row width to parent samples.
The bilinear sample budget adds the output row width to twice the parent samples.
This conservative estimate also covers boundary requests that reuse one row.
Each intermediate cost must pass the budget before another node can multiply it.
It bounds sample evaluations, not instructions, copy costs, or elapsed CPU time.
Every node must satisfy the limits, even when the output does not use that node.

## Cancellation and ownership

Execution checks a caller cancellation callback before validation and each node.
It also checks before each group of 64 output pixels.
Cancellation returns an explicit error and releases partial row storage.
The callback is cooperative. It cannot preempt a blocked or nonterminating callback.

Returned rows own their storage. Retained rows preserve their bytes after another row changes.
Materialized image sources retain their existing storage outside the scratch estimate.
No threading or concurrent mutation guarantee follows from these contracts.

## Verification

Thirteen cases cover virtual sources, eager agreement, budgets, graph safety, cancellation, and row isolation.
The virtual-source case represents ten million pixels and returns only a 2,000-byte row.
Depth tests execute a 64-node path and reject the next node.
Coordinate tests exercise i32 boundary dimensions without allocation of a large image.
Bilinear cases cover padded RGB/RGBA chains, single rows and columns, mixed-alpha goldens, and recursive work limits.
Cancellation cases retain the first input row or both input rows and a partial output row.

Native x86-64 and ARM64 under QEMU use the heap sanitizer.
The same cases also execute through Wasmtime.
These tests do not qualify aggregate memory or production service behavior.

## Future region engine

The future graph separates source, decode, orientation, resize, crop, color, and encode operations.
Each node must expose dimensions, format, alpha semantics, and required input regions.
A scheduler must enforce scratch budgets and release rows after their final consumer.

Codec access patterns determine whether a node can request arbitrary regions.
Sequential codecs cannot promise efficient arbitrary tile access.

Tiles, caching, fusion, and decoder row providers remain future work.
PNG decoding still materializes its complete decoded image.
The row interface does not assume that every codec supports efficient arbitrary regions.

First measure copy costs and decode access patterns before selection of a tile size.
