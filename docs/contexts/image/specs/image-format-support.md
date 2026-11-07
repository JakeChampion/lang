# Image format support

**Status:** In progress. Broader scope questions remain open.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The engine accepts a restricted set of PNG and JPEG files.
It has no QOI or JPEG XL codec.
Programs need reliable image input and output with clear compatibility and resource limits.

## Intended behavior

Extend the current engine to read and write PNG, JPEG, JPEG XL and QOI.
Retain explicit pixel formats, color interpretation, errors and admission limits.
Keep codec state local to each call and preserve caller-owned input.
Keep current APIs compatible while new interfaces mature.

## Delivery order

1. Add a complete QOI codec for RGB8/RGBA8, with explicit color-space metadata.
2. Expand PNG compatibility beyond the current noninterlaced RGB8/RGBA8 subset.
3. Expand JPEG compatibility and implement baseline image encoding.
4. Establish the JPEG XL architecture, conformance references and test corpus.
   Implement useful native decode and encode stages against that specification.
5. Add common format inspection and conversion interfaces where the concrete codecs justify them.

QOI provides the first independent end-to-end codec slice.
The current PNG and JPEG implementations remain the base for their extensions.
JPEG XL requires a separate format and dependency audit before an implementation promise.
The user can change this order through the open scope questions.

## Implementation decisions

- Reuse checked pixel layouts and owned image storage.
- Use pure Fern codecs under the current epic policy.
  The user can explicitly approve a different runtime dependency policy.
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

QOI validation covers every opcode, initial index state, modular channel arithmetic,
run boundaries, alpha changes and both color declarations.
It also covers padded source rows, exact stream termination, malformed headers and oversized dimensions.
Use the published QOI specification and the reference codec as independent checks.

## Out of scope for the first slice

HTTP/CDN service work, animation, implicit color transforms and production certification.
The broader image request remains active after the QOI slice.

## Pending questions

- Does the user want decode and encode together, or decode first?
- Must every runtime codec remain pure Fern, with JPEG XL under the same rule?
