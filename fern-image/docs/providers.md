# Execution row providers

Issue #13384 adds a source contract for external row implementations inside the Fern graph.
It does not add a PNG graph adapter or an incremental inflater.

## Factory and row contract

`from_provider` accepts output dimensions, a pixel format, a `ProviderSpec`, and ordinary pipeline limits.
Graph construction and metadata queries validate declarations without opening the provider.
The current unary operators select one reachable source. Unused provider factories remain unopened.

`ProviderSpec.open` receives the cancellation callback and returns a `RowProvider` or a `PipelineError`.
The engine invokes it once for each execution that reaches that provider.
Each invocation must create fresh provider state.
The graph stores the factory rather than its live session.
Every row, batch, or tile execution creates a separate engine session table.
Graph copies retain the factory value.

The provider reads a requested row index and receives the cancellation callback.
It returns `SourceRow` with the requested index and an owned one-row image.
The engine checks index, width, height, format, tight stride, declared byte count, and exact array length before pixel reads.
The supplied image must use the graph source width and format, with no row padding or extra stored bytes.

`ProviderFailure(string)` can preserve a provider-specific error.
`Pixel` reports invalid row storage. Invalid row metadata or coordinates return `InvalidGraph`.
`AccessPattern` reports unsupported source access.
Cancellation still returns `Cancelled`.
An error ends that execution and releases its private sessions and cache.

Factories and callbacks are trusted application code.
The engine cannot inspect their captures or enforce their declared internal memory and work costs.
Factories must return fresh state and honor their access and cost declarations.
External effects and caller-held values do not roll back after an error.
The interface provides no concurrent-execution or thread-safety guarantee.

## Access patterns and cache admission

`RandomRows` permits arbitrary requested row indices.
A fresh random-access session supports ordinary `render_row`, batches, tiles, and the fused variants.
A zero-byte cache remains valid for this access pattern.

`ForwardRows` permits nondecreasing provider read requests.
Nearest mappings remain monotonic, while bilinear execution can revisit a recent dependency row.
Forward execution therefore requires cache capacity for two complete rows at every node on the reachable path.
This allowance preserves the recent dependency rows for the current nearest and bilinear operators.
It remains conservative when fusion skips intermediate rows.

`required_cache` exposes this minimum payload allowance.
An insufficient batch or tile cache returns `Budget` before the factory opens.
An uncached `render_row` request for a forward source returns `AccessPattern`.
Use a batch or cached tile interface with the required cache for that source.
The engine also rejects a backward read that reaches a forward session.

This contract does not imply efficient arbitrary access for sequential decoders.
Future operators with different dependency windows need their own access and retention rules.

## Declared source costs

`storage_bytes` declares retained provider input or state payload.
`scratch_bytes` declares additional working payload beyond the ordinary source row allowance.
Both values must be nonnegative. They add to the graph scratch charge.
The ordinary source allowance still charges twice the tightly packed row size.

`samples_per_row` declares provider work per requested row and must be at least the source width.
`setup_samples` declares fixed initialization or prefix work for one execution.
The engine charges setup work once, without multiplying it through bilinear requests.
`required_samples` returns the per-output-row estimate. `required_setup_samples` returns the fixed charge.

Batch work equals the fixed setup charge plus the per-row estimate times the output count.
Cached tile work adds the number of copied tile pixels.
An uncached tile creates a separate random-access session for each output row and charges setup work for every session.
Admission includes the full cache allowance and the existing conservative row and tile scratch charges.
All admission checks finish before the factory opens or tile pixels allocate.

For one tested random provider, source storage is 32 bytes and extra scratch is 16 bytes.
A four-pixel RGBA row adds 32 ordinary row bytes.
A following two-pixel nearest node adds 16 bytes, for 96 logical scratch bytes.
Two output rows require eight setup samples plus twice six row samples, for 20 samples.
A one-column, two-row tile adds 16 scratch bytes and two copy samples, for 112 bytes and 22 samples.
The same uncached tile needs two setup charges, for 30 samples and the same logical scratch allowance.

These are declared logical charges, rather than measured allocator, RSS, or CPU limits.
They exclude capacity slack, metadata, consumer retention, and undeclared callback allocations.
The engine does not meter provider instructions or guarantee a hard deadline or recoverable allocation failure.
Physical scratch allocation, aggregate service admission, and production qualification remain unfinished.

## Ownership and checks

The current `Cell` contract rejects elements that contain function values.
The engine keeps an immutable session table outside the row cache cell.
Each session uses a scalar cursor cell for access checks.
Provider implementations can retain their own validated state inside their returned read closure.

The [capability probe](../experiments/provider_factories.fern) verifies returned closures, immutable session arrays, fresh cursors, and captured byte ownership.
It passes native Linux, Wasm, ARM64/QEMU, and the primary interpreter with compiler `5a5c96ff8665`.
Compiled probes report zero final live bytes.

The provider regressions cover independent RGB/RGBA coordinates, fresh executions, exact admission, invalid rows, and heap errors.
They also cover every cancellation poll, retained values, unused factories, and the 64-node graph limit.
A finite matrix checks all sixteen nearest/bilinear choices across four alternating scale stages.
Both formats and both fusion modes produce 64 combinations, each checked against eager rows and a cropped tile.
The synthetic providers also reject backward reads independently of the engine guard.

These checks do not qualify a codec, hostile provider code, hardware threading, or production service behavior.

## Integrated validation

Source `96923f511099` includes main `3cd4363213b2` and the three main CI repairs.
All 111 image TAP cases pass on native Linux, Wasm, and ARM64/QEMU with compiler `2505df503dc4`.
The compiler binary has SHA-256 `a837990c908f873362601964ccdd2bb5eb77dde78e984876f21f528346b4cff0`.
Its compiler and stdlib sources match that main snapshot.
Each completed test executable reports zero final live bytes.
The native script checks 27 executables, compiler rejection probes, and formatter behavior.

Native execution uses heap sanitization. Wasm provides the leak census without native heap fault detectors.
All 1288 repository source format checks pass.
These local results do not establish full GitHub CI success or any production gate.
