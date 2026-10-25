# Roadmap and proposed issues

M0, M1, and scalar M2 are implemented.
M1 provides scalar pixel color operations. Whole-image transforms remain future work.
These issue proposals are local documents. No GitHub issues were created.

## M1: Color primitives

Dependency: M0.
Implement RGB/RGBA conversion, grayscale, channel operations, and alpha operations in Fern.
Specify encoded sRGB versus linear values.

Test transparent black, retained color at zero alpha, partial alpha, saturation, and round-trip tolerances.
Document rounding and division by zero rules for alpha.
Gate: real native tests and no implicit color conversions.

## M2: Scalar resize

Dependencies: M0 and M1 alpha semantics.
Implement nearest-neighbor and bilinear resize with exact dimensions.
Specify pixel-center maps, edge clamps, rounding, intermediate precision, and alpha treatment.

Test 1x1 images, single rows, single columns, odd sizes, checkerboards, and extreme aspect ratios.
Enforce destination and scratch budgets before allocation.
Measure copies and memory before optimization.

Gate: golden outputs, bounds tests, and a reproducible baseline benchmark.

## M3: PNG feasibility and compression audit

Dependency: M0. Color conversion is optional for the first restricted subset.
Audit the current Fern DEFLATE implementation for zlib framing and strict output limits.
Compile small known-good and malicious compressed fixtures.

Specify signature, chunk order, lengths, CRC, and scanline reconstruction tests.
Scope the first decoder to noninterlaced RGB8/RGBA8.
Gate: verified bounded decompression before the decoder implementation begins.

No external codec dependencies without explicit approval.

## Memory qualification before production

Dependency: M0.
Measure shared and unique update costs, allocator overhead, and peak memory.
Verify a bounded allocator or define process isolation with recoverable worker failure.

Decide whether an opaque row builder can reduce copies without loss of safety.

## Later milestones

M4 adds a region pipeline with bounded scratch memory.
M5 adds JPEG baseline decode before encoder work.
M6 adds a signed HTTP service, secure origin fetch, and cache rules.

M7 adds independently tested WebP support.
M8 decides AVIF scope and dependency policy.
M9 qualifies tenant isolation, observability, resilience, and CDN integration.

Each milestone requires its own scoped review and acceptance tests.
