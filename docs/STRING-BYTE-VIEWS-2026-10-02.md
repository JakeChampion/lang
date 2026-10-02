# Allocation-free string byte views

The primary compiler now implements `s.as_bytes(): [u8]` without allocating
or copying, for both `string` and `str` receivers. The copying constructor
`s.bytes(): u8[]` keeps its existing behavior. This addresses the outstanding
runtime part of D8 in #5626 / #5632.

## Representation and ownership

A byte view occupies one value slot. An array-backed view holds the packed
array pointer. A string-backed view holds the string descriptor with bit 1
set. Native descriptors and padded WASM literals are aligned so that the bit
is available. The runtime cache key changes with this ABI.

Conversion retains the descriptor. Typed release removes the tag and chooses
the string-view or array release routine. Generic retains and uniqueness
tests mask the tag only while addressing the count; a retain returns the
original value. Stack runtime helpers, native register selectors, fused
uniqueness branches and WASM follow this rule. An optional typed alignment
proof lets native count operations on ordinary references omit the mask.

Typed source anchors keep backing storage live through calls, aliases,
aggregates and returns. Descriptors that can be handed back through a byte
view are excluded from frame placement. Array lending preserves its source's
ownership. Byte-view slicing keeps the existing copying contract and checks
bounds before allocating.

For scalar reads, lowering decodes a view's data pointer and length where
the value is defined, including parameter entry and phi-edge assignment.
Those scalar locals own nothing. Repeated reads use them directly, with no
reader call or layout dispatch in the scan loop. Checked indexing and
slicing use the same bounds abort as other array operations.

## Validation

`TestSelfHostByteViewRuntime` covers heap and literal strings, NUL, Unicode,
empty input, both receivers, aliases, records, tuples, arrays, maps, closures,
options, slices and loop-carried views that swap backing layouts. Allocation
counts exclude source construction. Sanitized runs require balanced counts
and zero live bytes. `TestSelfHostByteViewRC` checks tagged and ordinary
pointers through value and fused-branch uniqueness tests, alias retention,
final release and native alignment-proof selection.

The final pre-integration target group passed on x86-64 Linux, ARM64 Linux
and WASM in 169.850 seconds, including optimization shapes, physical RC,
helper resolution and lifting. Its lifetime group passed in 239.860 seconds,
followed by all lint gates. The earlier reader checkpoint passed the full
unit lane. Actual stage-2 Darwin/core-WASM probes pass, including the added
CRC32 case. The CRC32 fixture also passes on all three Linux/WASM targets
in 26.796 seconds. Integration checks follow before publication.

The pre-integration bootstrap reaches a byte-identical stage-2/stage-3 fixed
point at 13,072,817 bytes, SHA-256
`07f2eaaafdbc5a8aedbe61d29e5f7f827e36e87d055b9805a836a1ee3014c705`.
Native and WASM cross-version cache probes verify old cold/warm entries,
candidate invalidation, equality with clean output and reuse of new entries
across two linked units.

## Measurements

The prerequisite baseline compiler has SHA-256
`039fb2ee864414734ddef432570ec16ab9cb9d335c3a389c423fba85a9257eb9`.
The ARM64 Darwin benchmark converts a 4096-byte string and either reads its
length or scans its bytes. It checks results, warms each binary and alternates
five samples. A scale-1 pilot precedes scale 256, with 256,000 conversions per
sample. No other workstream compiler or test job ran during timing.

| Workload | Copying baseline median | Byte-view median |
| --- | ---: | ---: |
| Conversion and length | 41.708 ms | 3.492 ms |
| Conversion and scan | 678.155 ms | 553.784 ms |

These timings use candidate `e97d0d33`, before the final generated-release
alignment proof. Both program images occupy 49,713 bytes. Reproduce with:

```sh
uv run --no-project python tools/bench_byte_views.py \
  --before /path/to/baseline-compiler --after build/bootstrap/stage2 \
  --stdlib internal/stdlib --output /tmp/byte-view-pilot --scale 1 --samples 5
uv run --no-project python tools/bench_byte_views.py \
  --before /path/to/baseline-compiler --after build/bootstrap/stage2 \
  --stdlib internal/stdlib --output /tmp/byte-view-full --scale 256 --samples 5
```

The existing `std/hash.Crc32.update` consumer already uses `as_bytes()`.
Updating the same 4096-byte chunk allocates one block with the baseline and
zero with the candidate on Darwin and core WASM. Both produce checksum
3631681148 and byte count 4096, with all allocations released. The regression
now asserts the allocation-free update.

No binary-size baseline is changed by this work. The size audit separates
generated-code overhead from the added compiler implementation; final
integration measurements follow before publication.

D8's frontend-only E063 diagnostic parity remains acceptance work.
Production typed lowering already refuses a returned view whose source
would escape local storage. The epic stays open pending that work and the
remaining string-boundary changes.
