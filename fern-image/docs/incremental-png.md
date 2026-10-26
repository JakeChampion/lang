# Incremental PNG row output

Issue #13459 adds opt-in row output from the native DEFLATE reader.
The existing eager decoder, row sessions, folds, and graph provider preserve their contracts and budgets.
The supported PNG profile remains 8-bit RGB/RGBA with the documented restrictions.

Use `png.inspect_incremental_rows` for the full-range logical plan.
Use `png.open_incremental_rows` for a fresh forward session.
Use `png.fold_incremental_rows` for an owned accumulator and a selected row range.
Use `provider.incremental_source` for legacy or fused graph rows and cropped tiles.

## Validation and ownership

The factory prepares PNG chunks and copies the raw DEFLATE body into an owned buffer.
Its first reader validates the complete stream before any row callback.
It checks exact compressed consumption, strict zlib framing, Adler-32, filtered length, and every filter byte.
Checksum and framing failures precede filter errors, as in the eager path.
A bad final filter rejects a selection of an earlier row before the consumer runs.

The validation helper releases its reader before a second fresh reader reconstructs row prefixes.
The session retains the previous reconstructed row and requests chunks of at most 32768 bytes.
It does not retain the complete filtered frame.
The Adler update combines checksums from the shared `deflate.adler32` implementation.
The row decoder uses the same filter predictor as eager reconstruction.

This design decodes the raw stream twice.
The first row still waits for complete validation.
The encoded PNG and raw DEFLATE input remain owned buffers.
This API does not stream encoded input or promise lower latency, allocation counts, or RSS for every image.

Each session permits forward requests and repetition of the most recent row.
An older request returns `DecoderAccess`. Invalid bounds preserve the session.
Reentry from a cancellation callback returns `DecoderAccess` and preserves the outer read.

Cancellation and stream errors terminate the session. Later requests preserve that failure.
Drop all session aliases to release captured input, reader tables, history, and the previous row.

Function values stay outside mutable cells.
Private scalar/array cursor cells contain no function values and no callback cycle.
Input and returned row mutations preserve the session through copy-on-write ownership.
Each graph execution creates a fresh session after graph, cache, and budget admission.

## Logical admission

Let `F` be the exact filtered length and `R` the byte stride of one row.
Let `W` be the smaller of `F` and 32768.
The full-range plan charges these logical payload lengths:

- Three times the encoded PNG length covers input, collected IDAT bytes, and the raw-body copy.
- `W` covers reader history.
- 16384 bytes provide a conservative allowance for bounded DEFLATE tables and their temporary arrays.
- `2 * R + W + 1` covers two rows, an output chunk, and the filter byte.

The table allowance uses the current reader's fixed symbol and code-length limits.
Fixed trees use at most 288 literal/length symbols and 30 distance symbols.
Dynamic headers use at most 316 combined code-length entries.
The allowance is not an allocator cap.

Capacity slack, metadata, compiler-generated temporary copies, allocator overhead, and caller-held outputs remain excluded.
These lengths do not establish physical scratch, peak live bytes, RSS, or aggregate service limits.

For selected rows, logical work is the input length plus `F` plus `(first + count) * (2 * R + 1)`.
This charges a full validation pass and the decoded/unfiltered prefix.
It does not meter parser, CRC, Huffman, callback, or allocator CPU instructions.
The graph charges full-range work once as setup and source width for each requested row.
Its fixed storage and scratch declarations reproduce the same payload plan with the ordinary two-row allowance.

For the 79-byte RGB fixture, the incremental plan charges 16662 payload bytes, 27 scratch bytes, and 119 full-range work units.
A first-row selection charges 106 work units.
The legacy plan remains 184 payload bytes and 14 full-range work units.
The incremental option can cost more for small images.

## Cancellation and qualification

The raw reader checks cancellation every 256 state-machine steps.
Row reconstruction and validation also check byte progress.
PNG preparation and CRC scans remain bulk operations.
Adler processing uses a bounded chunk without an internal cancellation callback.
These checkpoints do not establish a hard deadline or recoverable allocation failure.

The targeted cases cover all filters and channels, forward sessions, exact budgets, graph admission, ownership, reentry, errors, and cancellation.
The graph matrix covers all three-stage nearest/bilinear combinations through legacy and fused rows and cropped tiles.
Wide independent fixtures cross chunk and history boundaries and make Adler A zero at the first chunk boundary.
The offline generator checks their exact pixels with the pinned Pillow reference.
Small fixtures cover every observed cancellation poll. Wide fixtures cover cancellation during partial row output.

Native, Wasm, and ARM64/QEMU probes provide different validation capabilities.
Wasm supplies the leak census without native heap fault detectors.
Finite generated fixtures do not qualify photographs, broad compatibility, security, performance parity, or production safety.

## Reference campaign

The extended corpus compares incremental full rows, a middle selection, legacy/fused graph rows, and cropped tiles with the offline Pillow reference.
All three targets pass 964 cases: 142 accepted images and 822 explicit codec errors.
Every accepted byte and negative error class agrees across native, Wasm, and ARM64/QEMU.
Each target uses 61 fresh processes. All 183 processes report balanced allocations and zero final live bytes.

The worker retains comparison outputs, so these records do not measure decoder retention or peak memory.

The records use source `a296f8a8fd690a4a10e068a4965a964e210dcf2d` after integration of main `4d2bb5262e8a4af6f72b7298520718af4f0dfddc`.
The clean compiler reports `a296f8a8fd69` with SHA-256 `5e29d4e9a6323191b135a0d0b4199dea216d50e43a3cd86ba1f4132a8d155285`.
Native records hash 301 tool/source inputs. Wasm and ARM64 records each hash 302 inputs, including their runner.
Every record includes all 163 workspace stdlib sources and verifies unchanged hashes before and after execution.

New records reside in `benchmarks/results/png-incremental-native.json`, `png-incremental-wasm.json`, and `png-incremental-arm64.json`.
Earlier PNG records retain their original source, compiler, and qualification scope.
See [PNG qualification](png-qualification.md) for reference pins, corpus construction, process limits, and reproduction commands.
Current-head GitHub CI remains a separate gate.
