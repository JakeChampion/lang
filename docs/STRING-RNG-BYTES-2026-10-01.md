# Owned random bytes

`std/rand.rng_bytes(state, n)` now returns `(i64, u8[])`. Arbitrary
pseudorandom data stays in byte storage throughout; the implementation
extracts the builder contents with `buf_take_bytes` before freeing it.
The draw sequence, advanced state and handling of nonpositive lengths
are unchanged. Returned arrays remain valid after the builder is freed.

Tests compare the bytes and advanced state with `rng_fill`, exercise
partial words and empty requests, and check aliases and copy-on-write.
The bootstrap interpreter, native and SSA backends, WASM, Darwin primary
compiler and primary interpreter pass. Primary Linux and WASM execution
uses strict semantic IR with an allocation census. The primary WASM
component probe passes. Final unit, lint and target checks pass with the
ARM64 alignment prerequisite integrated.

## Measurement

Both versions use the same primary compiler, including the ARM64 `.balign`
correction. The fixture draws 1 MiB per round and checks the same output.
A four-round pilot passed before scaling only the round count to 64.
Two warmups precede seven alternating measurements per version, without
sanitizer or allocation instrumentation.

| Version | Median | Range |
| --- | ---: | ---: |
| String result | 43.106 ms | 40.686-111.553 ms |
| Owned byte result | 37.290 ms | 31.894-44.119 ms |

The ranges overlap, so this run does not establish a speed change.

Native text grows from 18,084 to 18,236 bytes. The Clang-assembled objects
attribute the 152-byte increase to the 160-byte raw extraction helper and
an eight-byte reduction in its caller. Native data grows from 576 to 600
bytes, matching the additional 24-byte empty-array constant in the object.
Unwind sizes are unchanged. The new helper and array metadata implement
the owned-byte return contract; no size baseline is raised.

The initial native measurement showed 256 extra data bytes. That exposed
the assembler's `.balign` exponent bug. Fixing it removed 232 bytes of
avoidable padding before the measurements above.
