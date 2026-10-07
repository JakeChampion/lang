# Offline bounded DSP graph

**Status:** In progress
**Contexts:** Runtime allocation, immutable buffers, ownership verification
**Date:** 2026-10-07

## Problem

An allocation contract is useful for stateful sample processing only if the
whole graph can compose without allocating. Application authors also need to
know what that composition costs against ordinary indexed replacement.

## Solution

Compare a strict FIP graph with a direct ordinary loop over the same mono
floating-point input, gain, FIR filter, feedback delay, dry/wet mixer and limiter.
Allocate all buffers and filter state before the first processing call.

## User stories

1. An author can process bounded blocks while retaining filter and delay state.
2. A caller can retain a snapshot without changing its previous values.
3. A compiler contributor can reproduce allocation and composition behavior.

## Implementation decisions

Use deterministic finite input and fixed coefficients. The causal filter keeps
two previous gained samples. The ring delay keeps its cursor across blocks.
The limiter clips the mixed output to the unit interval. Output remains valid
until its graph is consumed; a retained value remains an immutable snapshot.

Construction checks block and delay capacities before allocating. Processing
checks the count and available input before changing filter or ring state.
The direct loop and staged graph perform the same arithmetic and state changes;
neither receives artificial allocation or opaque call boundaries.

## Testing strategy

Compare each output and final state against an independent scalar reference.
Pin impulses, silence, clipping, ring wrapping, block partition invariance,
partial and empty blocks, refused capacity/counts and retained snapshots.
Check allocation count from the first post-construction call, primary compiler
target parity and negative FIP contracts. Pilot native timing before scale and
retain raw samples, compiler identity and process-resource limitations.

## Out of scope

Audio devices, scheduling guarantees, networking, arbitrary nonfinite input,
resampling, hardware-specific SIMD and speculative shared DSP APIs.

## Open questions

No product decision is missing. Allocation and timing measurements determine
whether the staged representation earns its cost for this workload.
