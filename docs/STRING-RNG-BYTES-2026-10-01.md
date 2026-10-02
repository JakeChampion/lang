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

Refreshed on 2026-10-02 after integrating main `e7e6a51c2`. Both versions
use the same final stage-2 compiler, including the ARM64 `.balign` correction.
The fixture draws 1 MiB per round and checks the same output.
A four-round pilot passed before scaling only the round count to 64.
Two warmups precede seven alternating measurements per version, without
sanitizer or allocation instrumentation.

| Version | Median | Range |
| --- | ---: | ---: |
| String result | 33.647 ms | 33.352-34.939 ms |
| Owned byte result | 28.541 ms | 28.061-29.071 ms |

The ranges are separated in this run. This result is specific to this
fixture and host; task-owned compiler and test jobs were stopped.

Native code grows from 18,188 to 18,340 bytes. The earlier Clang-assembled
objects attributed the same 152-byte difference to the 160-byte raw extraction
helper and an eight-byte reduction in its caller. Native data grows from 576 to 600
bytes, matching the additional 24-byte empty-array constant in the object.
Unwind sizes are unchanged. The new helper and array metadata implement
the owned-byte return contract; no size baseline is raised.

Both executables occupy 49,713 bytes. Their SHA-256 hashes are
`95b38a9f8f8a64db59474b6147ec625628c61530a79eb762a74358cfb3b6d3aa`
before and
`a5d6cd0224a84c3e716a6dd4f7ff8ee7a543b97cfcdaf277a425bbe31c917e4f`
after. These binaries differ from the original measurements, which is why
the timing comparison was repeated.

The initial native measurement showed 256 extra data bytes. That exposed
the assembler's `.balign` exponent bug. Fixing it removed 232 bytes of
avoidable padding before the measurements above.
