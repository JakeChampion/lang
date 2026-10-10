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
ownership. Byte-view slicing checks bounds before allocating. A sub-range
of a byte view or of an owned `u8[]` is `__fern_str_borrow` over the
selected bytes, tagged as a string-backed view (#8635): on x86-64 and arm64
a counted box carrying the borrowed-data marker, whose last release frees
the box alone while the semantic anchors keep the source alive; on WASM a
copy, since an inline string has no data pointer to share.

For scalar reads, lowering decodes a view's data pointer and length where
the value is defined, including parameter entry and phi-edge assignment.
Those scalar locals own nothing. Repeated reads use them directly, with no
reader call or layout dispatch in the scan loop. Checked indexing and
slicing use the same bounds abort as other array operations.

## Validation

`TestSelfHostByteViewRuntime` covers heap and literal strings, NUL, Unicode,
empty input, both receivers, aliases, records, tuples, arrays, maps,
options, slices, sub-ranges read past their source's last use, and
loop-carried views that swap backing layouts. A sub-range's allocation is
pinned not to grow with its length on the register backends. Allocation
counts exclude source construction. Sanitized runs require balanced counts
and zero live bytes. `TestSelfHostByteViewRC` checks tagged and ordinary
pointers through value and fused-branch uniqueness tests, alias retention,
final release and native alignment-proof selection.

After integrating main `8e10c76fe`, the target group passed on x86-64 Linux,
ARM64 Linux and WASM in 180.860 seconds, including CRC32, optimization
shapes, physical RC, helper resolution, lifting and WASM alignment. The
lifetime group passed in 227.301 seconds. Checker differential, semantic
identity, lending and SSA ownership tests passed in 591.021 seconds. The
integrated tree also passes all lint gates. The earlier reader checkpoint
passed the full unit lane; the final pre-integration
runtime passed target, lifetime and all lint gates. Actual integrated stage-2
Darwin/core-WASM probes pass all five runtime fixtures, with balanced counts.

The integrated bootstrap reaches a byte-identical stage-2/stage-3 fixed
point at 12,480,129 bytes, SHA-256
`ef60e137fa8db9349fd067ad085d7c96485607d4dad1da87b6fcdbc914421439`.
Native and WASM cross-version cache probes verify old cold/warm entries,
candidate invalidation, equality with clean output and reuse of new entries
across two linked units.

## Measurements

The baseline is main `8e10c76fe`, built to its own byte-identical fixed point,
12,397,377 bytes, SHA-256
`50d5ef464f03aeeaef746f7378848c1f04554466870dcfab84535fa0f7d1e06b`.
The ARM64 Darwin benchmark converts a 4096-byte string and either reads its
length or scans its bytes. It checks results, warms each binary and alternates
five samples. A scale-1 pilot precedes scale 256, with 256,000 conversions per
sample. No other workstream compiler or test job ran during timing.

| Workload | Copying baseline median | Byte-view median |
| --- | ---: | ---: |
| Conversion and length | 40.167 ms | 3.164 ms |
| Conversion and scan | 677.997 ms | 563.174 ms |

These timings use the integrated candidate `ef60e137`. Both program images
occupy 49,713 bytes. Reproduce with:

```sh
uv run --no-project python scripts/bench_byte_views.py \
  --before /path/to/baseline-compiler --after build/bootstrap/stage2 \
  --stdlib internal/stdlib --output /tmp/byte-view-pilot --scale 1 --samples 5
uv run --no-project python scripts/bench_byte_views.py \
  --before /path/to/baseline-compiler --after build/bootstrap/stage2 \
  --stdlib internal/stdlib --output /tmp/byte-view-full --scale 256 --samples 5
```

The existing `std/hash.Crc32.update` consumer already uses `as_bytes()`.
Updating the same 4096-byte chunk allocates one block with the baseline and
zero with the candidate on Darwin and core WASM. Both produce checksum
3631681148 and byte count 4096, with all allocations released. The regression
now asserts the allocation-free update.

No binary-size baseline is changed by this work. Compiling the same main
source with each generator separates the generated-code cost from the
added implementation. These are x86-64 Linux compiler images:

| Generator | Compiler source | Bytes |
| --- | --- | ---: |
| Main | Main | 11,009,240 |
| Candidate | Main | 11,091,416 |
| Candidate | Candidate | 11,121,232 |

The 111,992-byte increase consists of 82,176 bytes in generated code and
29,816 bytes from the added implementation. The generated-code changes
include counted borrowed descriptors, tag-aware retains and uniqueness,
and typed array-view lowering. Alignment proofs avoid imposing the new tag
mask on typed ordinary references and generated container-release helpers.

D8's frontend-only E063 diagnostic parity remains acceptance work.
Production typed lowering already refuses a returned view whose source
would escape local storage. The epic stays open pending that work and the
remaining string-boundary changes.
