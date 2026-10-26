# Sequential PNG rows

Issue #13341 belongs to row milestone #13215 and PNG milestone #13214.
The restricted PNG module provides `fold_rows` for sequential row consumption.
It uses the same chunk checks, strict zlib decode, and filter predictor as the eager decoder.
The supported PNG formats and metadata restrictions remain unchanged.

## Access and ownership

`fold_rows` accepts borrowed input bytes, a first row, a positive row count, pixel limits, and row limits.
It also accepts a cancellation callback, an initial state, and a consumer callback.
The consumer receives the state and one owned image value for each selected row.
It returns the next state or its own error.

Each image has the original width, a height of one, and tightly packed RGB8 or RGBA8 bytes.
The API reconstructs rows from zero through the end of the selection.
It emits only the selected rows, in ascending order.
An earlier row can supply filter dependencies without a consumer call.

The input and previously returned values remain unchanged after a consumer modifies its row through the pixel API.
The consumer can retain rows in its state.
Those retained values have a storage cost outside the row plan.
Success returns `RowFold[S]` with the final state and the admitted `RowPlan`.

## Admission

The first row must be nonnegative and less than the image height.
The count must be positive and at most the image height minus the first row.
The existing pixel limits still validate dimensions and decoded storage.

`RowLimits` contains positive `max_payload_bytes` and `max_row_work` values.
The default value for each limit is 134217728.
The payload limit counts bytes. The work limit counts byte operations rather than a memory quantity.

The plan charges these logical payload bytes:

```text
2 * input length + complete filtered frame + 2 * row stride
```

The input allowance covers compressed-byte preparation.
The row allowance covers the previous row and the current row.
The API checks the input allowance before chunk preparation.
It checks the complete plan before decompression and row reconstruction.

The plan charges this row work:

```text
image height + (first row + row count) * row stride
```

The first term counts validation of every filter byte.
The second term counts reconstruction of the required prefix bytes.
Neither term measures CRC work, decompression work, callback work, or elapsed CPU time.

The charges exclude allocator capacity slack, metadata, caller aliases, and callback state or retained rows.
They do not establish a physical scratch limit, peak allocation limit, RSS bound, or aggregate process bound.
The allocator still has no recoverable allocation-failure contract through this API.

## Validation, errors, and cancellation

The decoder materializes the entire filtered frame before row callbacks.
It validates every scanline filter byte before the first callback.
A bad final-row filter therefore rejects an earlier selection without a callback.
The current bulk inflater and chunk parser remain eager.
The [PNG provider adapter](png-providers.md) connects fresh sessions to the row graph.
The inflater remains eager.

`RowError[E]` distinguishes codec errors, invalid bounds, admission failures, cancellation, and consumer errors.
A failure returns no partial success state.
Earlier callback effects and externally retained rows remain with the caller.

The decoder polls cancellation before preparation, before decompression, during filter checks, during row reconstruction, and after each consumer call.
It checks every 64 filter bytes and every 256 reconstructed bytes.
It does not poll inside the bulk chunk parser or inflater.
These checks provide cooperative cancellation, without a hard deadline guarantee.

## Evidence

Eight targeted cases cover all five filters in RGB/RGBA, selected rows, borrowed input windows, and row mutation.
They also cover exact admission limits, invalid bounds, consumer failure, cancellation, malformed input, and final-filter validation.
All eight pass on native Linux, Wasm, and ARM64 under QEMU with zero final live bytes.
Native and ARM64 checks use heap sanitization. Wasm provides the leak census only.

The differential worker also emits full folded rows and a middle row selection.
The offline collector compares both outputs with independent Pillow bytes.
Every negative case must produce the same codec error class through the eager and row APIs.
The [PNG qualification record](png-qualification.md) describes the finite corpus and its limits.

The compiler needs the header parser repair in PR #13356 and the generic payload/ownership repair in PR #13364.
The row API uses the repaired language features directly.
