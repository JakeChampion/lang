# ICC APP2 payload assembly

`packages/codecs/jpeg/icc_app2.fern` joins caller-supplied ICC APP2 payloads into owned profile bytes.
It does not traverse JPEG markers, validate a profile, authorize a transform, or change encoded-image admission.
Existing JFIF/RGB interfaces still reject ICC and Exif metadata.
The [ICC envelope inspector](icc-envelope.md) remains an explicit separate call after assembly.

## API and framing

`assemble(payloads, limits, cancelled)` borrows a view of `Payload` records, each with an owned `bytes` array.
Each array excludes the APP2 marker and its two-byte length field.
Every supplied payload must contain the exact 12-byte `ICC_PROFILE` identifier with its null terminator, followed by one-byte sequence and total fields.
Sequence numbers start at one, and all totals must agree.
Duplicates, missing fragments, invalid indices, malformed lengths, and unrelated identifiers return explicit errors.
Physical input order does not determine profile byte order.

No supplied payloads return `Ok(None)`.
A complete sequence returns `Ok(Some(bytes))`, including a complete sequence whose data is empty.
Empty fragments are structurally permitted.
Neither an empty result nor arbitrary nonempty bytes imply a valid ICC profile.
Malformed fragments never become absence, and this API does not silently discard a profile or infer sRGB.

## Logical limits and admission

Defaults permit 1 MiB of profile data and 32 chunks.
Caller allowances must be positive.
Wire ceilings permit 255 chunks, 65,533 bytes per payload, and 65,519 data bytes per chunk.
The absolute assembled-data ceiling is 16,707,345 bytes.
Larger caller allowances do not raise these ceilings.

Complete preflight checks every header, declared count, duplicate, completeness rule, and i64 byte sum before output allocation or callbacks.
It also checks declared totals against the caller chunk allowance.
The output contains only the concatenated data bytes in sequence order.
All input and output remain eager, and caller-supplied input storage already exists before this call.

These are logical byte/count limits.
They exclude array capacity, record metadata, compiler temporaries, allocator overhead, and extra caller retention.
They do not establish a physical/process bound, a CPU bound, or a hard deadline.
The runtime allocator does not return a recoverable allocation error through this API.

## Ownership and cancellation

The output remains private until success, and ordinary ownership releases partial output after cancellation or failure.
Input alias updates through callbacks preserve the observed sequence fields and data bytes through COW.
Retained output aliases remain independent, and later calls use fresh output storage.
Callbacks remain outside Cells.

Absence uses two cancellation polls.
A present sequence with `n` assembled bytes uses `3 + ceil(n/64)` polls.
Polls occur before and after output allocation, before each global group of 64 copied bytes, and before success.
Malformed admission uses no callbacks.
The bounded metadata preflight and sequence searches do not introduce additional polls.
Cancellation remains cooperative rather than preemptive.

## Finite qualification method

Eight TAP groups pass on native, Wasm, ARM64/QEMU10, and the Go interpreter.
The Go checker accepts the same source.
Compiled unit heaps are nonzero and balanced with zero live blocks.
Tests cover absence versus empty data, unordered fragments, every identifier byte, framing prefixes, coherence, exact limits, 255 chunks, COW, and every unit poll.
They also check that assembly success and structural profile validity remain separate results.

The reference generator constructs declared profile bytes, partitions, and physical permutations independently.
Compact deterministic recipes and hashes reproduce the actual input and expected files.
The 368 cases include the full 16,707,345-byte container boundary, individual segment boundaries, malformed records, caller limits, and cancellation.
Seven offline Pillow metadata observations include unordered fragments and an empty fragment.
Those observations do not define malformed-input policy, complete JPEG admission, profile validity, or color accuracy.

The standalone worker compares every actual output byte with an independent expected file before it reports a complete matched-byte count.
The record's `profile_sha256` identifies that expected file.
Three deliberate controls alter expected bytes at offsets 0, 64, and 129 while the API input remains unchanged.
Each control requires the exact mismatch offset, byte values, expected nonzero exit, and a nonzero balanced zero-live census without a sanitizer finding.
An unrelated failure cannot qualify a control.

Positive workers process at most eight inputs and must exit zero with exact status, error, size, byte-comparison count, and poll fields.
The harness uses a predeclared 120-second process timeout for large container cases.
This test resource control does not create an API hard deadline.
Eight fault-control groups preserve field, row, error, reference, process, sanitizer, heap, and rejection-reason requirements.

Records checkpoint active work and complete validated prefixes through atomic file replacement.
Failures preserve failed output and return nonzero.
Complete status follows all worker checks, cleanup, and immutable source/tool checks.
The collector refuses an existing output record, so each qualification run needs a fresh output path.
Published historical datasets remain unchanged.

The initial native smoke uses uncommitted source and remains provisional.
Final qualification requires committed source, all three compiled targets, full regressions, and an exact-head normal merge.
Profile models, transforms, encoded integration, photographs, performance, security, compatibility, and production acceptance remain open.
No external runtime codec or CMM dependency forms part of this API.

## Reference

The framing derives from the JPEG/JFIF section of the [ICC profile embedding technical note](https://archive.color.org/files/technotes/ICC-Technote-ProfileEmbedding.pdf).
The official public web document supplies the identifier, sequence/count fields, segment limits, and theoretical profile-data ceiling.
This evidence contains no local PDF digest or access-bypass claim.
The [ICC decision](icc-feasibility.md) records the separate future acceptance gates.
