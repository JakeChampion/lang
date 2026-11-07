# Explicit RGB to JFIF samples

**Status:** Internal prerequisite in progress.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The component encoder accepts explicit sample planes.
Color image output needs a defined conversion from RGB samples before component assembly.
The inverse kernel does not provide this direction.

## Intended behavior

Add explicit RGB8 to full-range JFIF YCbCr conversion in pure Fern.
Use the printed decimal equations from JFIF 1.02, page 3.
Evaluate them exactly, round to the nearest integer with half ties upward, then clip.
The scalar result names Y, Cb and Cr separately.
The row API accepts packed RGB and returns packed Y, Cb, Cr samples.

## Contracts

Do not infer alpha, profiles, gamma transforms or encoded component meaning.
The input already has the RGB interpretation that the caller selects.
Do not choose chroma filters, sample factors or public JPEG defaults.

Keep borrowed input intact. Return independent owned row output.
Reject empty or partial samples before allocation or callbacks.
Apply caller byte admission and the existing conversion cap of 64 MiB.

Check cancellation before and after allocation, within each group of 64 samples, and before success.
Do not expose partial output after an error.
The callback and allocator do not provide a hard time or physical memory bound.

## Validation

Use independent Fraction arithmetic for the printed coefficients.
Compare every output byte exactly on native and Wasm.
Include all red/green pairs at blue 128, endpoint grids, neutral samples and deterministic random samples.
Test positive and negative chroma half ties, clipping, channel order, input aliases and independent output.
Test malformed shapes, exact byte admission and every cancellation poll.
Run Linux correctness checks and the full image suites before publication.

## Remaining scope

This checkpoint does not implement a complete color JPEG encoder.
Public JPEG options and alpha behavior remain open.
The user selected explicit integer and floating-point formats in the shared pixel model.
Retain the existing RGB8/RGBA8 APIs as that model expands.
Full PNG, JPEG and JPEG XL support and comparable performance remain active requirements.
