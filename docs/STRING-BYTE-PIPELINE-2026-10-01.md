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

`BufWriter.flush()` now extracts bytes and writes them through the raw API.
The primary interpreter uses host byte methods for stdin, stdout and stderr,
including short-write counts and close errors. Builder extraction also uses
the host API, replacing the temporary direct-memory compatibility path.

## Validation scope

The foundation passed the following validation at main `1ae9cadf6` before
the final integration described below:

- Full unit suite and `make lint-all`, with `GOMAXPROCS=2` and one Go
  package at a time.
- Bootstrap interpreter and target tests, primary Darwin tests, and the
  Linux x86-64, ARM64, core-WASM and component suite.
- A composed Reader-to-builder-to-Writer round trip over empty input and
  8256 raw bytes. The builder is freed before its extracted array is
  written. Primary native and core-WASM executions require balanced
  allocation counts and no sanitizer findings.
- Bootstrap from the official pinned seed, followed by `make distcheck`.

The interpreter bridge uses the published
[`stage0-20261001-c891ebc` seed](https://github.com/JakeChampion/lang/releases/tag/stage0-20261001-c891ebc).
Darwin runs exercise interpreters built through both Go and the primary seed;
Linux runs exercise primary ARM64 and x86-64 interpreters. All compare exact
output against the Go interpreter for readers, writers, builder reuse,
buffered writes and empty/nonempty pipeline round trips. Compiled buffered
writes and the pipeline also pass on ARM64, x86-64 and core WASM, with balanced
allocation counts on the semantic path. The bridge also passes the full unit
suite and `make lint-all` with the published seed pinned.

Integration with main `539e4e6e5` includes the view-method repairs and the
retirement of the old AST ownership route. The tests label default and
legacy-environment configurations explicitly because both now use production
typed IR. Direct stage-2 checks pass all six byte fixtures on Darwin and core
WASM binaries with exact output and balanced allocations. The composed
pipeline covers empty input and 8256 bytes.

The repaired compiler's Darwin bootstrap stages 1, 2 and 3 are identical at
14,577,249 bytes, SHA-256
`49eb1af37647e98ae7debcbd718d1479f3071a0e404fa2f580039e5d2b789026`.

That direct check also found a Reader overflow guard emitted as flat
`unreachable` inside a folded WAT branch. Wasmtime accepted it, but the
primary binary assembler exited while encoding it. The guard now uses folded
syntax. A regression builds and runs the pipeline with `-emit core-module`.
It passes with the primary Linux compiler and the actual Darwin stage 2.
The full Linux target matrix and `make lint-all` pass after the repair.

The Darwin Go harness is currently blocked before tests by a host `ENFILE`
linker failure. Docker's shared mount also returned `ENFILE` during the full
source walk, so the full Linux unit and lint gate runs against an isolated
snapshot inside the container. These infrastructure failures are not counted
as passing validation.

The target matrix covers bootstrap and primary native code, strict
semantic IR ownership checks, WASM Preview 1 and actual Preview 2
components. Fixtures include every byte value, malformed and split UTF-8,
aliases, range boundaries, short reads and writes, empty operations,
closed descriptors and payloadless results stored in a map. A deterministic
component host checks partial failures, error-handle disposal and scratch
allocation balance.

Primary Preview 2 components do not yet provide stdin Reader imports;
the raw reader tests require a clear refusal there. That path is not counted
as successful execution coverage. Primary interpreter file-handle opening
also remains unsupported; this bridge covers the standard streams.

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
