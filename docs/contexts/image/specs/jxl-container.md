# JPEG XL container foundations

**Status:** Locally qualified. Internal prerequisites only.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem and scope

The engine has no JPEG XL codec or checked container primitives.
The user requires complete decoding and encoding in pure Fern.
Container and codestream work need separate boundaries so that box framing does not imply pixel support.

This checkpoint adds exact signature classification, box header inspection, bounded envelope scans and box output.
It retains opaque payload bytes, caller aliases and explicit limits.
It does not choose a public image model, metadata policy or conversion rule.

## Box contracts

The header reader supports short lengths, extended unsigned lengths and a final box that extends to the closed input end.
It checks the complete header and declared extent before narrowing or sample access.
Empty payloads are valid. Short declared sizes and truncated payloads fail explicitly.
The result contains offsets and lengths into the caller's borrowed input.
It does not allocate a payload copy.

The scanner checks caller input-byte and box-count limits.
It returns bounded descriptors only after the complete envelope sequence validates.
The writer takes an explicit header form and exact output-byte limit.
Its result owns independent storage. It preserves payload bytes and type values exactly.
Cancellation returns no partial public result.

These limits describe logical input, descriptor count and encoded output.
They do not establish aggregate process memory or CPU bounds.
The module does not infer payload semantics from unknown box types.

## Complete JPEG XL scope

The current published file format is ISO/IEC 18181-2:2026, edition three.
The reference decoder supports file-format versions zero and one.
Version one permits partial codestream boxes to arrive out of order.
Do not treat version-zero-only framing as complete JPEG XL support.

Later container stages must validate signature and file-type ordering, brands, versions and complete or partial codestream assembly.
They must cover metadata, compressed metadata, frame indexes, levels, JUMBF and exact JPEG reconstruction data.
Unknown metadata needs an explicit preservation contract.
The existing pure Fern Brotli modules are candidates for compressed metadata after strict framing qualification.
They must not become external runtime dependencies.

Codestream work still requires Modular and VarDCT, precision, color, extra channels, frames, animation and every applicable coding tool.
Only the complete format and conformance audit can establish final feature completion.

## Validation

Use independent integer byte equations and the pinned libjxl implementation as offline references.
Test all header forms, empty payloads, borrowed windows, concatenated boxes and exact round trips.
Check all short length forms, extended unsigned boundaries, every header prefix, truncated payloads, cancellation and resource limits.
Verify opaque payloads that resemble nested headers without interpreting them.
Run small campaigns before larger inputs and counts.
Run applicable native, Wasm and Linux tests with balanced heaps.

No speed, complete-container or image-decoding claim follows from this prerequisite.
Public pixel-model and conversion questions remain open.

## Local checkpoint evidence

The frozen reference campaign passes 581 cases on native ARM64 Darwin and actual Wasm.
Of these, 227 have valid generic envelopes and reproduce the complete input bytes.
The 25 real containers retain exact reference pixels after Fern rewrites each box.
They include all six orders of a three-fragment version-one codestream.
The other cases test exact signature results, malformed extents and resource admission.

The full image suites pass 467 native cases and 480 Wasm cases.
The seven box groups also pass on Linux ARM64 and x86 through QEMU.
All final heap counts are positive, balanced and zero-live.
The Python controls pass 305 cases. Strict complexity, source, type and formatter checks pass.

`fern-image/tests/qualify_jxl_boxes.py` requires explicit paths to libjxl 0.12.0 and its reference tools.
`fern-image/tests/reference_jxl_boxes.c` uses only its public box API for offline inspection.
The collector records source and tool hashes, commands, exact descriptors, box bytes and reference pixels.
Reference libraries do not enter production or normal Fern tests.
The local receipt is `image-jxl-boxes-20261107/summary.json` under the Fern proof directory.

References: [JPEG workplan](https://jpeg.org/jpegxl/workplan.html), [published file-format edition](https://www.iso.org/standard/91379.html),
and [libjxl 0.12.0 decoder](https://github.com/libjxl/libjxl/blob/v0.12.0/lib/jxl/decode.cc).
