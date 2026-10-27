# Restricted ICC v4.4 envelope inspection

`packages/codecs/icc/envelope.fern` inspects standalone profile bytes.
It admits only the v4.4 version encoding, input/display class, RGB data, and XYZ PCS.
Other version, class, color-space, or PCS values return explicit scope errors.
This subset does not imply that other ICC profiles are invalid.
Existing JFIF/RGB decode entry points continue to reject ICC metadata.

## API and result

`inspect(input, limits, cancelled)` borrows a byte view and returns owned `ProfileInfo` or `IccError`.
The result contains the byte count, class, preferred intent, creation fields, flags, device attributes, ID bytes, and ordered tag metadata.
Each tag reports its unsigned signature, checked offset/size, and opaque type signature.
The result also reports the number of unique data elements.
Input bytes do not remain attached to the result.
The caller can retain or update metadata without changing another result or input.

The preferred intent is metadata, not an executed color request.
`unverified_id` copies the header ID without an MD5 check or authenticity claim.
Registry membership, required tags, curve contents, and type-specific payload rules remain unchecked.
Even an empty directory can pass structural inspection.
Success does not authorize a transform or establish full profile conformance.

## Admission and layout

Positive caller byte/tag allowances precede callbacks and output allocation.
Defaults are 1 MiB and 128 tags, with absolute logical ceilings of 16 MiB and 1,024 tags.
Allowances above the ceilings do not raise those ceilings.
Unsigned 32-bit lengths, offsets, counts, and additions use i64 before checked conversion.
The file must contain exactly its declared byte count.

Header checks include the file signature, supported scope, reserved fields, intent range, fixed-point PCS illuminant, and date/time field ranges.
The date policy requires a nonzero year, month 1–12, day 1–31, hour 0–23, and minute/second 0–59.
It does not check month-specific calendar validity.
Vendor bits in flags and attributes remain opaque.
The illuminant must round to the required four-decimal D50 values.

The inspector preserves directory order and permits exact offset-and-size aliases.
It rejects duplicate tag signatures, partial overlaps, unaligned starts, invalid extents, gaps, and excess trailing bytes.
Every element needs its common eight-byte type prefix, with four zero reserved bytes.
Unknown type signatures remain opaque.
Padding must contain zero bytes and end at the next four-byte boundary.
Disjoint padded spans must cover the complete data region exactly once.

The validator compares at most 1,024 entries pairwise before it creates the result arrays.
It does not sort or copy payloads.
The second pass copies only metadata and ID bytes.
These logical policies exclude array capacity, compiler temporaries, allocator overhead, and extra caller retention.
They do not establish physical/process bounds, a CPU bound, or a hard deadline.

## Cancellation and ownership

Cancellation polls occur after byte/tag admission, before each tag, and after each 64 prior-entry comparisons.
Additional polls precede output construction, precede each copied tag, and precede success.
Cancellation during private output construction releases partial metadata through ordinary ownership.
The callback remains outside every Cell.
All calls use fresh state, and input aliases retain copy-on-write isolation during callbacks.

For a successful directory with `n` entries, the poll count is `3 + 2n + sum(ceil(i/64), i=0..n-1)`.
Early malformed-input errors can precede some of these polls.
No poll can convert an invalid or cancelled input into success.

## Qualification

Eight TAP groups cover constructed layouts, the frozen offline LittleCMS profile, scope errors, unsigned extents, padding, aliases, ownership, and cancellation.
The same groups run on native, Wasm, ARM64/QEMU10, and the Go interpreter.
All unit cases retain strict error and metadata assertions.
One initial fixture mutation also overwrote the file signature, so that fixture received a narrower seconds-field mutation.
The initial failure log remains available, and no compiler defect or validation relaxation follows from it.

`tests/generate_icc_envelope.py` constructs independent wire vectors and explicit error mutations.
The vectors include unordered entries, shared spans, all frozen-profile prefixes, unsigned overflow, D50 rounding edges, and exact hard limits.
The generator declares expected metadata and closed-form poll counts without calling the Fern inspector.
Applicable offline Pillow/LittleCMS observations cover the frozen profile and reordered directory entries.
Synthetic envelopes lack required-tag semantics and do not receive an offline conformance claim.

`scripts/test-icc-envelope.sh` runs eight inputs per fresh worker and records exact metadata, strict errors, and nonzero balanced heap censuses.
Every worker must exit zero without a sanitizer finding.
The collector retains failed observations and complete prefixes, then exits nonzero on any mismatch.
Protocol fault controls check field changes, case identity, missing/extra rows, errors, process exits, and heap requirements.
Final records require a committed source and immutable selected source/tool hashes throughout each campaign.

This finite evidence does not qualify tag semantics, color transforms, JPEG admission, general compatibility, security, performance, or production use.
Curve/model work remains #13659, while APP2 assembly remains #13660.
The broader [ICC decision](icc-feasibility.md) defines the separate future acceptance gates.

### Recorded finite observations

Final records use committed source `d0ced552cc685a375a61f2ad8e55697aa6d5c09f`.
Each target checks 857 vectors: 147 accepted envelopes and 710 expected errors.
Each target also repeats eight applicable offline LittleCMS metadata observations.
All metadata fields, error classes, and poll counts match exactly, and all three target sample records are equal.
The reference bundle reproduces byte-for-byte with SHA-256 `a9f57f4a7842781e5b47352352cea972ead4c8862897ec9aefd3b444619a55a0`.

The campaigns use 108 fresh workers per target, for 324 total workers.
Every worker reports nonzero balanced allocations and frees, with zero live blocks.
The native record preserves 1,247 selected source/tool hashes, while each runner record preserves 1,248.
These include all compiler and 163 stdlib sources, worker binaries, fixture/generator/collector sources, Python/Pillow, and the offline LittleCMS library.
All selected hashes remain unchanged throughout each campaign.

The clean compiler source is `933843468f0bf6ef3d782c85b9e4d348ca312182`.
Its SHA-256 is `6602687e6c7ed8e67633bbc3b627b4ab61a9a461359d31bb090f248782b08610`.
Compiler and stdlib sources match the campaign checkout.
The full native and Wasm scripts pass, and standalone ARM64 execution covers 33 executables.
All three targets pass 235 TAP cases with 72/48/33 balanced censuses and zero live blocks.
Standalone ARM execution does not establish completion of the whole Go ARM harness.

Both unchanged compiler/stdlib complexity checks pass without skips.
All 2,042 eligible Fern format checks, build, vet, lint, CI self-tests, runtime bundle, protocol controls, and STE checks pass.
The raw records are [native](../benchmarks/results/icc-envelope-native.json),
[Wasm](../benchmarks/results/icc-envelope-wasm.json), and
[ARM64](../benchmarks/results/icc-envelope-arm64.json).
Historical records remain unchanged.

## References

The layout derives from [ICC.1:2022](https://www.color.org/specifications/ICC.1-2022-05.pdf), sections 4.2, 7.1–7.4, and the common tag prefix rules.
The official document was read through the public web source.
Executor PDF access returned HTTP 403, so no local PDF digest forms part of this evidence.
No credentials, proxy, headers, or network policy changed for access.
