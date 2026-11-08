# Adam7 PNG input

**Status:** Locally qualified checkpoint. Publication waits for the preceding image PR.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem and intended behavior

The current PNG parser rejects Adam7 images before pixel reconstruction.
The user requires complete PNG input and output in Fern.
This checkpoint adds eager Adam7 input for every currently supported sample mode.
It does not select a new public pixel model or conversion policy.

## Contracts

The pass planner follows the seven-pass pattern in the PNG third edition.
It derives each pass extent, packed row stride, filtered byte count and stream offset with checked arithmetic.
Empty passes consume no filter byte or data.
The total filtered data obeys the current caller limit and 64 MiB ceiling.

The decoder inflates one complete stream and resets the previous filter row at each nonempty pass.
It reconstructs bytes before packed grayscale or palette expansion.
Transparency compares original samples before expansion.
Each pixel lands at its exact pass coordinate in the admitted final image.
Complete compressed framing, chunk checks and exact filtered length remain mandatory.

The existing sequential, incremental and graph row APIs retain explicit Unsupported results for interlaced input in this checkpoint.
They must not silently allocate a complete pixel image under their row-only memory plans.
Progressive and row contracts remain work for the complete feature scope.
The existing encoder retains its noninterlaced output until explicit encoder options mature.

## Validation

Test all small pass geometries against the normative eight-by-eight assignment pattern.
Check every currently supported color/depth combination, all five filters, empty passes, narrow dimensions, packed padding and transparency.
Use independent Python equations, zlib and offline Pillow or libpng for exact pixels.
Reject bad filters, short or excess pass data, malformed palettes, chunks and limits.
Verify input aliases, repeated calls, exact admission and row API rejection.
Run applicable native, Wasm and Linux tests with balanced heap reports.

The checkpoint makes no speed rank or physical memory bound claim.
Complete precision, metadata, animation, output options and other PNG requirements remain open.

Reference: [PNG third edition](https://www.w3.org/TR/2025/REC-png-3-20250624/), sections 7.2, 8.1 and 9.

## Qualification checkpoint

The frozen fixture set contains 66 independent images across all eleven current color/depth combinations.
The broader campaign checks 132 images and 12 malformed inputs on native and Wasm after a sixteen-case smoke run.
The earlier PNG campaign still passes all 1940 cases on each target.
Its malformed interlace-header fixture retains exact eager Scanline and row/provider Unsupported expectations.
All other prior rejection comparisons remain unchanged.

The new geometry module passes strict complexity checks.
All new PNG helpers stay below that limit.
The prior PNG module retains seven older complexity violations, with none added or increased.
The parser's measured complexity decreases from 74 to 70 after extraction of final admission.
No suppression or limit changes apply to this work.

All 460 native image cases in 65 programs and 473 actual Wasm image cases pass.
Linux ARM64 and x86-64 each pass 69 targeted groups.
All 290 Python controls, source checks, formatter, type, workflow, shell and prose checks pass.
Heap reports remain positive, balanced and zero-live.
The compiler, runtime, public image declarations and noninterlaced encoder source remain unchanged.
