# Raw byte pipeline for D9

Binary consumers need an owned byte path before their temporary strings
can be removed. This prerequisite for #5714 adds five operations across
the compiler implementations:

| Operation | Contract |
| --- | --- |
| `buf_take_bytes(builder)` | Return an independent `u8[]` snapshot and reset the builder length, retaining its capacity. |
| `buf_push_bytes_range(builder, bytes, lo, hi)` | Append a borrowed range, clamped to the array bounds; an empty or inverted range does nothing. |
| `Reader.read_chunk_bytes(n)` | Return owned bytes or an I/O error. EOF is an empty array. |
| `Writer.write_bytes(bytes)` | Borrow the array and complete short writes; return an error on failure or zero progress. |
| `Writer.write_some_bytes(bytes)` | Borrow the array and return a backend write count or an I/O error. |

These paths never construct a string from the raw input. Empty I/O calls
still observe host errors. Retained arrays survive later reads, builder
reuse and disposal. Closed descriptors report errors.

The component output prerequisite chunks writes to the host's 4096-byte
limit, traverses all iovecs, reports partial counts and errors, and releases
scratch memory and owned error handles. The WASM raw writer also fixes the
payloadless success box's `None` tag.

## Validation scope

The combined branch passed validation after integration with main
`1ae9cadf6`:

- Full unit suite and `make lint-all`, with `GOMAXPROCS=2` and one Go
  package at a time.
- Bootstrap interpreter and target tests, primary Darwin tests, and the
  Linux x86-64, ARM64, core-WASM and component suite.
- A composed Reader-to-builder-to-Writer round trip over empty input and
  8256 raw bytes. The builder is freed before its extracted array is
  written. Primary native and core-WASM executions require balanced
  allocation counts and no sanitizer findings.
- Bootstrap from the official pinned seed, followed by `make distcheck`.
  Stage 2 and stage 3 were byte-identical at 14,593,777 bytes, SHA-256
  `6a0ca5f5adff2011f823c7b71491c948106ba5010239a89dc36977945d8b8626`.

The target matrix covers bootstrap and primary native code, strict
semantic IR ownership checks, WASM Preview 1 and actual Preview 2
components. Fixtures include every byte value, malformed and split UTF-8,
aliases, range boundaries, short reads and writes, empty operations,
closed descriptors and payloadless results stored in a map. A deterministic
component host checks partial failures, error-handle disposal and scratch
allocation balance.

Two existing primary-compiler gaps remain explicit. Its interpreter needs
a raw Reader/Writer host bridge after the bootstrap seed can compile those
calls. Its Preview 2 components do not yet provide stdin Reader imports;
the raw reader tests require a clear refusal there. Neither path is
counted as successful execution coverage. Primary builder extraction does
run in the interpreter, and component output is covered.

The focused commits retain their individual measured reports:

- [Builder extraction](STRING-BUILDER-BYTES-2026-10-01.md)
- [Byte-range append](STRING-BYTE-RANGE-2026-10-01.md)
- [Raw Reader](STRING-READER-BYTES-2026-10-01.md)
- [Raw Writer](STRING-WRITER-BYTES-2026-10-01.md)
- [Component output](STRING-COMPONENT-WRITES-2026-10-01.md)

Each performance comparison uses the same compiler for both programs at
its preparation revision. Those measurements do not claim a new timing
result for this combined integration, and no size baseline is changed.

This is a prerequisite, not completion of D9. Binary consumers still need
migration, and the remaining text producers need their validity contracts
enforced before #5714 and epic #5626 can close.
