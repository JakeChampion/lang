# Extract builder contents as bytes

`buf_take_bytes(b): u8[]` returns an independently owned snapshot and resets
the builder length. The builder retains its capacity. Subsequent pushes,
takes and freeing the builder cannot change the returned array. Empty takes
return an empty array. No intermediate string is constructed, so arbitrary
bytes can pass through this API without violating the string invariant.

The operation is implemented in the bootstrap backends and interpreter,
and the Fern compiler's interpreter, typed lowering and native/WASM
backends. Runtime helpers are emitted only when used. Extraction initializes
array metadata and copies the payload without first zero-filling bytes that
the copy will overwrite. The Fern interpreter retains a compatibility
implementation using the existing host builder layout so the pinned stage0
compiler can still build it.

Tests cover all 256 byte values, empty extraction, mixed push operations,
builder growth, repeated short takes from a large reserve, aliases and
builder reuse/free. Primary compilation uses strict semantic IR on Linux
x86-64, Linux arm64, Darwin arm64 and WASM, with sanitizer and balanced heap
census checks. Both interpreters and bootstrap/SSA backends pass. The IR
registry and existing builder emission tests pass.

The official Darwin bootstrap uses the pinned stage0 with `STAGE0` unset.
Stage1 builds and passes compiler and `tr` smoke tests. Stage2 and stage3
are byte-identical after integration with main at 14,543,425 bytes, SHA-256
`508b7f67b99bb7d306b575e9559cbc1cf6f379668abe4b26991cf1db1413a117`.
Native and WebAssembly target tests pass on that integration, including
primary pure components and stdout components with both lowering modes.
The complete integrated unit suite passes. Lint initially encountered the
host's system-wide open-file limit during source checks; the full
`make lint-all` retry with reduced host concurrency passes.

## Extraction measurement

Measured on arm64 macOS on 2026-10-01 before the final main integration.
Both programs use the same new primary
compiler and strict semantic IR. They repeatedly push an ASCII block into
a retained builder, extract it and check the first and last byte. The old
path uses `buf_take(b).bytes()`; the new path uses `buf_take_bytes(b)`.
ASCII keeps both programs within their respective input contracts.

The pipeline passes at 16 rounds before scaling to 4,096 rounds; only the
round count changes. Each round processes 65,536 bytes. Two warmups precede
seven samples, alternating program order. Sanitizer, leak census and RC
debug instrumentation are absent. Other validation jobs were active.

| Path | Median | Range |
| --- | ---: | ---: |
| String then bytes | 32.956 ms | 32.712-35.063 ms |
| Direct bytes | 22.661 ms | 22.090-24.469 ms |

All outputs agree and the measured ranges do not overlap. This is an
extraction workload, not a claim about every binary I/O consumer.

Native text shrinks from 18,544 to 18,024 bytes. Object text also shrinks
by 520 bytes: removing the string conversion wrapper and runtime saves
596, the new extraction helper adds 160, the caller saves 80, and layout
alignment saves 4. Constants, data and BSS are unchanged. Compact unwind
shrinks by 64 bytes and EH frames by 96. No size baseline changes.
