# Append raw byte ranges

`buf_push_bytes_range(b, bytes, lo, hi)` copies a range of a borrowed byte
array into the builder. It clamps `lo` to zero and `hi` to the array length;
empty or inverted ranges append nothing. Later source mutations and builder
growth cannot change previously appended bytes. The operation pairs with
`buf_take_bytes` without constructing intermediate strings.

Bootstrap and primary native backends use bulk copying. WASM and both
interpreters implement the same bounds and ownership contract. The primary
interpreter uses existing host byte-push calls so the pinned stage0 compiler
can still build it. IR extension 351 is stable; reserved gaps for the
separate byte I/O APIs are not registered prematurely.

Shared tests cover all 256 byte values, negative and extreme bounds, empty
and inverted ranges, native word boundaries, repeated growth, independent
builders and source aliases. They pass through bootstrap native/SSA and
WASM backends, both interpreters, and the primary compiler on Darwin arm64,
Linux x86-64, Linux arm64 and WASM. Primary tests exercise legacy and strict
semantic lowering with sanitizers and balanced allocation counts. The IR
registry test passes. WASM components also run both builder fixtures in
both lowering modes, with and without stdout. The component gate admits
these local memory operations without requiring host imports. Component
execution is checked separately from the core-module allocation census.

The pinned Darwin stage0 builds stage1 in 64 seconds; compiler and `tr`
smoke tests pass. Stage2 and stage3 are byte-identical at 14,344,993 bytes,
SHA-256 `40826e1ceb47691da94a74a4a7271c720fd2a65b017dbf2b8c1ef2ef44ef5831`.
The complete unit suite and `make lint-all` pass.

## Bulk-copy measurement

Measured on arm64 macOS on 2026-10-01. Both programs use the same new primary
compiler with strict semantic IR. A 65,536-byte input has malformed UTF-8
at its first and last bytes. Each round appends it to a retained builder,
extracts bytes and checks both endpoints. The old path calls `buf_push_byte`
for each byte; the new path calls the range operation once.

The full pipeline passes at 16 rounds before scaling to 4,096, changing
only that count. Two warmups precede seven samples, alternating program
order. Sanitizer, heap census and RC debug instrumentation are absent.
Other validation jobs were active.

| Path | Median | Range |
| --- | ---: | ---: |
| Per-byte append | 321.550 ms | 312.017-365.797 ms |
| Range append | 22.953 ms | 22.446-25.565 ms |

All outputs agree and the measured ranges do not overlap. This measures
the builder operation, not end-to-end utility performance.

Native text grows from 19,128 to 19,216 bytes. Object text also grows by
88 bytes: the range helper adds 216, the old append loop removes 136, and
the caller adds 8. The new helper checks bounds and copies the range in
bulk. Constants, data and BSS are unchanged; compact unwind shrinks by
32 bytes and EH frames by 48. No size baseline changes.
