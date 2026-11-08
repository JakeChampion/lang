# JPEG XL dimension and animation headers

**Status:** Locally qualified. Internal prerequisite only.
**Context:** Fern-native image engine.
**Date:** 2026-11-08.

## Scope

Use the qualified raw and integer fields for image dimensions, preview dimensions and animation descriptors.
Implement both directions in pure Fern.
These internal descriptors do not allocate an image or expose a new public image model.
Complete pixel, precision, frame and container support remains open.

## Contracts

Readers borrow bytes and accept an explicit bit position and closed logical extent.
They return complete descriptors and exact next positions.
Readers accept legal noncanonical forms. Truncated fields fail without access beyond the logical extent.
Every descriptor retains its full representable unsigned values.

Image and preview dimensions have distinct integer distributions and division rules.
Preserve the fixed aspect ratios and their exact integer truncation rule.
Canonical writers select the same first ratio as the pinned reference.
Use its compact dimension form when that form can represent every required field.
Use the legal undivided preview form when the divided form cannot represent a dimension.
Reject zero dimensions and values that cannot fit a complete header.

Dimension descriptors use signed 64-bit fields to retain inferred widths beyond signed 32-bit storage.
No dimension descriptor admits a pixel allocation or changes existing image limits.
The eventual image decoder must apply caller limits before allocation and sample access.

Animation descriptors retain the rational tick frequency, unsigned loop count and timecode flag.
Reject values outside the header distributions before output preparation.
Frame timing, composition and actual animation output remain separate work.

Writers return raw fields for the existing bounded byte writer.
The byte writer provides output admission and cancellation.
Each header operation uses a bounded number of fields and exposes no partial public image.
The stage introduces no public alpha, conversion or JPEG preset policy.

## Validation

Use independent integer equations and pinned upstream header classes as offline references.
Compare exact values, positions and canonical bytes in both directions.
Cover compact dimensions, all ratios, asymmetric dimensions, preview distributions and unsigned loop counts.
Test each bit truncation, malformed input, value boundary and retained alias.
Include actual independently encoded JPEG XL files for dimension inspection.
Run small complete campaigns before larger corpora, then native, Wasm and applicable Linux tests.

Reference source: [libjxl headers](https://github.com/libjxl/libjxl/blob/v0.12.0/lib/jxl/headers.cc).
This stage makes no complete-conformance, physical-memory or performance-rank claim.

The optional fixture generator records macOS load commands and resolved reference libraries.
Routine qualification uses frozen JPEG XL files and pinned source code on Linux or macOS.
External reference code and fixture tools are outside the runtime dependency graph.

## Local evidence

The full image suites pass 488 native cases and 501 actual Wasm cases.
Each target passes 3,699 reference cases after a 16-case campaign.
Each full campaign contains 3,654 exact accepted outputs and 137,736 logical queries.
The pinned classes check 32,912 physical-input reads and 3,699 encode requests per target.
Four real codestream fixtures supply independent image dimensions.
All twenty-two upstream source hashes remain exact.

Thirty-two cases expose the reference setup limitation for divided previews.
The reference reader accepts the legal undivided forms and reproduces their exact bytes.
These cases remain separate from malformed input and real format rejection.

Linux ARM64 and x86-QEMU each pass six new groups.
All final heaps are positive, balanced and zero-live.
All 347 Python controls, source checks, lint-fast, formatter, type, strict module complexity, shell and workflow checks pass.
The compiler and stdlib retain the exact qualified primary source inventory.
Earlier image runtime and test sources retain their parent hashes.

The guarded local receipt is image-jxl-headers-20261108/summary.json.
The fixture manifest records twenty resolved reference dependencies at generation time.
It records the generator, options and exact input and output hashes.
This finite evidence does not establish complete JPEG XL conformance or a performance rank.
