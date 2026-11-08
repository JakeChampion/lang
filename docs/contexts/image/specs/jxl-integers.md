# JPEG XL integer fields

**Status:** Locally qualified. Internal prerequisite only.
**Context:** Fern-native image engine.
**Date:** 2026-11-08.

## Scope

JPEG XL headers need selector-based unsigned integer codes.
Extend the qualified raw bit module with U32 distributions and the fixed U64 code.
Both directions use pure Fern. Existing public image policies remain unchanged.
The complete format, precision, container and pixel requirements remain open.

## Contracts

Readers borrow bytes and take a position and closed logical bit extent.
They return the integer and the exact next position after a complete field.
Truncated fields fail at the logical extent without access beyond it.
Readers accept all representations that the format permits, including longer representations of small integers.

U32 codes use exactly four explicit distributions.
A direct distribution contains one unsigned 31-bit value.
A range distribution contains a width from one through 32 and an unsigned 26-bit offset.
Validate all four distributions before input access or output preparation.
Range decoding adds the payload and offset modulo two to the power of 32, as the reference reader does.

The U32 writer selects the shortest representation and then the first selector on a tie.
It rejects values outside all four encodable ranges.
The U64 writer represents every unsigned 64-bit value with the shortest form.
Its reader stops after the final four payload bits without an extra stop bit.

Writers return bounded raw fields for the existing byte writer.
The byte writer supplies exact allocation admission, cancellation and zero final padding.
Each integer operation has a fixed maximum number of fields.
The internal stage introduces no unbounded input count or implicit image allocation.

## Validation

Use independent integer equations and pinned upstream integer coders as offline references.
Cover every selector, width, offset boundary, tie, continuation boundary and unsigned maximum.
Compare exact bytes, values and consumed positions in both directions.
Test each truncated prefix and logical bit extent, malformed distributions and retained aliases.
Run a small complete campaign before the larger corpus.
Run native, Wasm and applicable Linux cases with final heap checks.

Reference source: [libjxl integer coders](https://github.com/libjxl/libjxl/blob/v0.12.0/lib/jxl/fields.cc).
This stage does not implement bundle extensions, floating header fields or image headers.
It makes no complete-conformance, physical-memory or performance-rank claim.

## Local evidence

The full image suites pass 482 native cases and 495 actual Wasm cases.
Each target passes 4,552 reference cases after a 16-case campaign.
Each full campaign contains 4,491 exact accepted outputs and 189,591 logical queries.
The pinned coders confirm 44,104 physical-input reads and 4,536 canonical size or selector choices per target.
All eighteen upstream source hashes remain exact.

Linux ARM64 and x86-QEMU each pass the eight new groups.
All final heaps are positive, balanced and zero-live.
All 333 Python controls, source checks, lint-fast, formatter, type, strict module complexity, shell and workflow checks pass.
The core compiler and stdlib retain the exact qualified primary source inventory.
Earlier image runtime and test sources retain their parent hashes.

The guarded local receipt is image-jxl-integers-20261108/summary.json.
Reference source code serves only as an offline test tool.
The evidence is finite. Complete JPEG XL headers, entropy, pixels and container semantics remain open.
