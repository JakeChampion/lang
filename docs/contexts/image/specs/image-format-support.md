# Image format support

**Status:** In progress. Format and dependency scope confirmed.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The engine accepts a restricted set of PNG and JPEG files and complete QOI pixel streams.
It has no public JPEG image encoder or JPEG XL codec.
Programs need reliable image input and output with clear compatibility and resource limits.

## Intended behavior

Extend the current engine to read and write PNG, JPEG, JPEG XL and QOI.
Retain explicit pixel formats, color interpretation, errors and admission limits.
Keep codec state local to each call and preserve caller-owned input.
Keep current APIs compatible while new interfaces mature.

Completion requires full feature coverage for each requested format, implemented in Fern.
Intermediate restricted modes are delivery checkpoints.
The performance objective is the fastest implementation in the world, supported by reproducible measurements.

## Delivery order

1. Add a complete QOI codec for RGB8/RGBA8, with explicit color-space metadata.
2. Expand PNG compatibility beyond the current noninterlaced RGB8/RGBA8 subset.
3. Expand JPEG compatibility and implement baseline image encoding.
4. Establish the JPEG XL architecture, conformance references and test corpus.
   Implement useful native decode and encode stages against that specification.
5. Add common format inspection and conversion interfaces where the concrete codecs justify them.

QOI provides the first independent end-to-end codec slice.
The current PNG and JPEG implementations remain the base for their extensions.
JPEG XL requires a full format and architecture audit under the pure Fern requirement.
The user confirmed decode and encode for all four formats on 2026-11-07.

## Implementation decisions

- Reuse checked pixel layouts and owned image storage.
- Always implement image features and runtime codecs in pure Fern, including JPEG XL.
  The user confirmed this standing requirement on 2026-11-07.
  External runtime codec libraries, bindings and helper processes are excluded.
- Preserve encoded color-space declarations without implicit conversion.
- Check dimensions and arithmetic before allocation.
- Apply caller limits to input, decoded storage and encoded output.
- Propagate cancellation without a partial public image.
- Reject unsupported modes explicitly.
- Keep independent codec libraries outside the runtime dependency graph.

## Validation

Each codec needs independent valid vectors and malformed-input tests.
Lossless formats need exact pixel comparisons and round trips.
Lossy codecs need predeclared comparisons against independent references.
Tests cover truncation, lengths, overflow, limits, cancellation and caller aliases.

Run applicable native and Wasm tests with heap checks.
Measure memory and performance before any optimization claim.
Do not weaken original assertions or increase baselines to obtain a pass.

## Complete coverage and performance

Inventory each format's defined modes, sample depths, color interpretation, metadata and other features.
Track missing decode and encode coverage until the full acceptance scope is complete.
Retain explicit current limitations in documentation throughout the work.
An internal transform or restricted codec does not complete a format milestone.

Establish reproducible native benchmarks against leading independent implementations.
Compare equal feature sets, input classes, output quality and resource conditions.
Measure throughput, latency and memory with exact source, compiler, hardware and option records.
Use those results to select algorithms and optimizations in Fern.
Verify correctness and feature coverage after each optimization.
Keep the fastest-in-the-world objective separate from any unmeasured performance claim.

QOI validation covers every opcode, initial index state, modular channel arithmetic,
run boundaries, alpha changes and both color declarations.
It also covers padded source rows, exact stream termination, malformed headers and oversized dimensions.
Use the published QOI specification and the reference codec as independent checks.

## Out of scope for the first slice

HTTP/CDN service work, animation, implicit color transforms and production certification.
The broader image request remains active after the QOI slice.

## Confirmed scope

The user selected explicit integer and floating-point formats in the shared pixel model on 2026-11-07.
Retain the existing RGB8/RGBA8 APIs.
Specify exact sample precision, storage, conversion and admission before implementation.
Use this shared model for full PNG and JPEG XL support.

The PNG sample-transparency checkpoint extends the existing RGBA8 model with grayscale and RGB tRNS.
It retains original samples, unused-bit masks and exact admission checks.
This checkpoint does not complete PNG coverage. Full decode and encode remain the acceptance scope.

- PNG, JPEG, JPEG XL and QOI each require decode and encode.
- All image implementations and runtime codecs remain pure Fern.
- Independent codec libraries remain offline test references only.
