# JPEG XL sample precision

**Status:** Locally qualified. Internal prerequisite only.
**Context:** Fern-native image engine.
**Date:** 2026-11-08.

## Scope

Extend the qualified integer fields with an internal sample-precision descriptor.
Implement both directions in pure Fern.
Retain integer or floating-point declaration, total sample bits and exponent bits.
This descriptor does not allocate pixels, convert samples or expose a new public image model.
Full pixel, color, frame and container support remains open.

## Contracts

Readers borrow bytes and accept an explicit bit position and closed logical extent.
They return complete descriptors and exact next positions.
They reject truncated fields before access beyond the logical extent.
They accept legal noncanonical distributions and reject invalid semantic ranges.
Use executable upstream checks to resolve inconsistent source comments.

The original class accepts integer precision from one through thirty-one bits.
It accepts floating-point exponent sizes from two through eight bits.
The sign occupies one bit. The mantissa occupies two through twenty-three bits.
The floating-point exponent field occupies four bits and stores the exponent size minus one.
The integer form ignores its unused exponent input and returns zero.

These are format-header rules, not shared pixel storage limits.

Writers validate the complete descriptor before output preparation.
They return bounded raw fields for the qualified byte writer.
They select the shortest distribution and the first selector on a tie.
They do not change shared pixel formats or set a public NaN policy.
Raw sample words retain their independent contract.

## Validation

Compile the original pinned upstream class and field coders as offline references.
Use independent integer equations for exact values, positions, bytes and invalid declarations.
Cover every small range, selector, mantissa boundary, bit truncation and logical extent.
Run a small complete pipeline before the full corpus.
Run native, actual Wasm and applicable Linux tests with source and heap guards.
Retain independent real codestream probes where their public APIs support each declaration.

Reference sources: [metadata class](https://github.com/libjxl/libjxl/blob/v0.12.0/lib/jxl/image_metadata.h)
and [metadata fields](https://github.com/libjxl/libjxl/blob/v0.12.0/lib/jxl/image_metadata.cc).
The stage makes no complete-conformance, physical-memory or performance-rank claim.

## Local evidence

All 494 native image cases in seventy programs and 507 actual Wasm cases pass.
Each target passes 3,047 reference cases after a sixteen-case campaign.
Each full campaign checks 2,900 exact accepted outputs and 95,860 logical queries.
The original class checks 30,084 physical-input reads and 3,047 canonical encoder outcomes per target.
Eight independently encoded JPEG XL files supply real precision declarations.
The reference retains all forty-five source hashes from the compiler dependency manifest.

The original metadata and field sources also compile from that closed source set under Linux GCC and Clang.
Both Linux references pass 1,320 range requests.
The pure Fern module passes six groups on both Linux ARM64 and x86-QEMU.
All final Fern heaps are positive, balanced and zero-live.
All 365 Python controls, source checks, lint-fast, formatter, type, strict module complexity, prose, shell and workflow checks pass.

The optional fixture generator records resolved macOS library dependencies.
Its manifest records eight dependencies, generator and adapter hashes, basic information, full files and extracted codestreams.
Some higher-precision reference files use a container.
The offline generator extracts their complete jxlc payload for this internal test.
Routine native and Wasm qualification uses frozen files and pinned source code.
External codec code remains outside the runtime dependency graph.

The guarded local receipt is image-jxl-depth-20261108/summary.json.
The compiler and stdlib retain the exact qualified primary source inventory.
Earlier image runtime and test sources retain their parent hashes, except the explicit test-script integration.
Full pixel, conversion, frame and container support remains open.
