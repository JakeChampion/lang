# Resumable DEFLATE output

Issue #13428 adds an opt-in raw DEFLATE reader to `std/deflate`.
It supports stored, fixed-Huffman, and dynamic-Huffman blocks.
The existing eager decoder shares the bit reader, Huffman construction, and dynamic table validation.
PNG providers still use an eager filtered frame. This reader is a prerequisite for a later integration.

## API and ownership

`deflate.inflate_reader(input, max_out)` accepts owned encoded bytes and a nonnegative total output limit.
It returns an `InflateReader` or `ReadRequest` for a negative limit.
The reader captures the complete encoded input.
Input mutations through another alias preserve the session through copy-on-write ownership.

The `read` callback accepts a chunk size from 1 through 32768 and a cancellation callback.
Success returns an `InflateChunk` with owned bytes, `done`, `consumed`, `total`, and `history_bytes`.
The byte count never exceeds the requested chunk size.
The history length never exceeds 32768 bytes.
Length/distance copies resume across chunk boundaries and permit overlap within the history ring.

`total` counts the decompressed bytes so far.
`consumed` counts the compressed bytes that the parser read.
A pending match can emit more bytes without another compressed input byte.
Only `done` identifies the exact compressed-stream endpoint.
Trailing bytes remain outside that consumed count.
A completed reader returns a stable empty completed chunk on later valid requests.

A Huffman end code can require another request after an exactly full chunk.
An empty chunk therefore occurs only at completion.
The total output limit can be zero for a valid empty stream.
The reader rejects another output byte when the total reaches its limit.

Reader aliases share one cursor. A new factory call creates an independent session.
Retained output chunks remain owned values after later reads and mutations.
Drop all reader aliases to release the captured input and session state.

## Errors and cancellation

`ReadCodec` wraps the existing `InflateError` variants.
Malformed input, truncation, and output-limit failures terminate the session.
Later valid requests return the same codec error.
An invalid chunk size returns `ReadRequest` without advancing the session.
Recursive reads from a cancellation callback return `ReadBusy` without disturbing the outer request.
This guard does not establish thread safety.

The decoder checks cancellation at request execution and every 256 state-machine steps.
An empty-block chain still performs these checks.
A dynamic header has finite code-table loops but no internal cancellation callback.
Cancellation terminates the session with `ReadCancelled` and discards the current partial chunk.
Later requests return `ReadCancelled`.
A completed or failed reader preserves its terminal result without another cancellation callback.

An earlier successful chunk can precede a later stream error.
The API does not retract bytes that a caller consumed before that error.
A caller that needs whole-stream validation must observe `done` before it accepts a complete output.
Eager and resumable decoders can report different error priorities after partial output.

## Table validation

Issue #13430 records a valid literal-only dynamic block that the eager decoder previously rejected.
RFC 1951 section 3.2.7 permits an empty distance alphabet when no match uses it.
The shared validator now accepts that alphabet and still rejects a subsequent distance use.
Incomplete nonempty alphabets require exactly one one-bit code.
Oversubscribed tables, a missing end code, and invalid incomplete alphabets remain errors.

The shared parser rejects headers with more than 286 literal/length codes or 30 distance codes.
Independent Python zlib checks cover the accepted fixture and malformed controls.

## Logical storage and remaining work

The implementation retains the complete encoded input, Huffman tables, and at most 32768 history bytes.
A request creates at most 32768 output bytes.
Caller-held chunks and input aliases add separate retention.
Array capacity slack, metadata, allocator overhead, and temporary record copies remain outside these length counts.
These counts do not establish a physical allocation limit, process RSS bound, or aggregate service bound.

This API does not stream encoded input or implement incremental zlib/gzip wrapper validation.
It does not yet replace the eager filtered frame in PNG providers.
No hard CPU deadline, recoverable allocation failure, worker isolation, performance parity, or production safety follows.
The implementation and tests use Fern. Python zlib supplies an offline reference only.

## Tests and records

Eight TAP cases cover independent streams, six chunk sizes, empty blocks, exact consumption, and history wrap.
A manual stream uses distance 32768 after a stored block. Python zlib independently verifies its output.
Other cases cover literal-only tables, malformed controls, input/output ownership, aliases, invalid requests, every cancellation poll, and callback reentry.
The Go harness also executes the existing eager DEFLATE suite on native Linux and Wasm.

The separate finite corpus uses Python zlib for expected output, stream endpoints, and rejection outcomes.
It includes level 0, 1, 6, and 9 streams, fixed and default strategies, seeded noise, periodic bytes, and pathological sizes.
It includes flush boundaries, empty blocks, maximum-distance copies, trailing streams, every truncated prefix, and finite bit mutations.
Each worker checks five chunk sizes, payload lengths, total counts, history length, and monotonic compressed consumption.
The worker retains output bytes for comparison. These tests do not measure the memory use of discarded chunks.

The collector requires balanced allocations and zero final live bytes for every fresh batch.
It hashes the compiler, worker, collector, runner, Python, zlib module when separate, all compiler sources, and all stdlib sources.
It checks every hash after the campaign and verifies that input files remain unchanged.
A failure preserves the current batch and manifest beside the requested record.
Native and Wasm CI run the campaign and upload their raw records.

Run these commands from the repository root:

```sh
FERN="$PWD/bin/fern" ./fern-image/scripts/test-deflate-reader.sh native
FERN="$PWD/bin/fern" ./fern-image/scripts/test-deflate-reader.sh wasm
QEMU_AARCH64=/path/to/qemu-aarch64 FERN="$PWD/bin/fern" ./fern-image/scripts/test-deflate-reader.sh arm64
```

Use the pinned Wasmtime toolchain. ARM64 records use QEMU and do not qualify hardware performance.
Native and ARM64 builds use heap sanitization. Wasm provides the leak census only.
The campaign is finite. It does not establish broad compatibility, unbounded fuzz coverage, security, or performance parity.

## Recorded qualification

The records use source `eb2ab979dd9cbbe14a9905bf014ebdfcab5c6bce` after integration of main `57dfc7233201` and checker fix #13443.
The clean compiler reports `eb2ab979dd9c`.
Its SHA-256 is `0639c3d5362211d38bd16f386037770fd91a6f71a64dd2669ea963c5b3e4b8c5`.

Each target passes 1641 raw streams across 206 fresh processes.
Each target accepts 829 streams and rejects 812 streams.
All 618 processes report balanced allocations and zero final live bytes.
All three targets produce identical sample records.
The records hash 163 stdlib sources and all compiler Fern sources.

The raw records reside in `benchmarks/results/deflate-reader-native.json`, `deflate-reader-wasm.json`, and `deflate-reader-arm64.json`.
The PNG regressions use separate `png-inflate` records and preserve earlier observations.
Each PNG target passes 964 cases across 61 fresh processes with zero final live bytes.
These regressions still use the eager PNG provider.

The native and Wasm image scripts pass all 126 image and shared-reader TAP cases.
The native script checks 29 executables, compiler refusal probes, and formatter behavior.
The Go harness passes four reader/eager target subcases and both complexity gates without skips.
The gates use the current upstream baselines without changes from this PR.
The direct Go checker accepts the reader without an overlay.
The primary interpreter also returns the expected literal-only output.

ARM64 records use QEMU 10.0.13 from the Debian package `qemu-user` version `1:10.0.13+ds-0+deb13u1`.
The package SHA-256 is `ca6ede739327a20ae5a3498230c8a3a9a072c586e131bc6bcc1201eb1725e336`.
The runner SHA-256 is `73cc2584f119f9e74c85c0a8549dbb6fd104ae3897a06a7500797f9e5b4d0943`.
Signed Debian package metadata supplies the expected package checksum.
This runner also passes 200 repeated executions of a preserved failure fixture.

Two earlier campaigns with QEMU 7.2 stopped with intermittent `SIGSEGV` at different batches.
The preserved batches pass individually, and repeated runs also reproduce the failure with the earlier compiler output.
These failed campaigns are not successful validation.
The cause remains unconfirmed. The completed records identify the newer runner explicitly.
These local results do not establish full GitHub CI success.
