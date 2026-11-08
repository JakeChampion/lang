# Private packed QOI pixels

**Status:** Locally qualified checkpoint. Publication waits for the preceding image PR.
**Context:** Fern-native image engine.
**Date:** 2026-11-07.

## Problem

The native allocation probe shows costs that grow with the pixel count.
The frozen 256x256 RGBA noise input causes 65563 allocations for one encode and 65552 for one decode.
The current private codec state uses pixel records with four channel fields.
Test whether a packed private word removes allocation overhead from those records.
Do not infer a cause or a performance result before measurement.

## Contracts

Use one private unsigned word with red, green, blue and alpha in successive bytes from high to low.
Keep the initial pixel at zero RGB with alpha 255 and the initial index at all-zero words.
Retain modular byte arithmetic, all six QOI operations, exact run lengths and complete termination checks.
Keep explicit RGB/RGBA and color-space declarations, exact limits and cancellation poll behavior.
Preserve the public pixel model, QOI API, input aliases and independent output ownership.
Do not change compiler or runtime code.

Use a private unsigned token word and an explicit byte count instead of temporary token arrays.
The closed encoder supplies counts from one through five.
Retain byte order, exact output admission and the existing cancellation polls.

## Validation

Run the existing QOI tests and pinned independent reference on native and Wasm.
Check exact output bytes against the frozen baseline for all benchmark fixtures.
Retain complete image suites, source checks and applicable Linux execution.
Use the unchanged benchmark workers and inputs for before/after observations.
Collect separate sanitized allocation counts and balanced final heap records.
Run a small campaign before larger observations.

Host load and limited repetitions prevent a performance-rank claim.
Allocation counts and bump-byte deltas do not establish a physical memory bound.
Reject the experiment if it changes correctness or does not improve the measured cost.
Full image format coverage and the fastest implementation objective remain open.

## Measured observations

The separate sanitized probe uses the same compiler and exact RGBA noise inputs before and after the change.
It measures one encode and one decode per input.
The complete encoded bytes and final pixels remain identical.

| Side | Encode allocations before | Encode allocations after | Decode allocations before | Decode allocations after |
| --- | --- | --- | --- | --- |
| 16 | 275 | 19 | 272 | 16 |
| 32 | 1045 | 21 | 1040 | 16 |
| 256 | 65563 | 27 | 65552 | 16 |

All three final heap reports balance with zero live bytes.
These counts describe the selected inputs and compiler.
They do not establish a universal allocation count or physical memory bound.

The native pipeline checks 72 observations at each side with three measured iterations and two warmups.
All complete encoded outputs match the frozen baseline.
Both versions use the same worker, compiler, reference codec and inputs.
Host load remains uncontrolled, so the time observations do not establish a performance rank.

The new regression groups check every output-byte budget and every cancellation poll in the selected token corpus.
The independent QOI campaign passes 48 bidirectional cases on native and Wasm.
Linux ARM64 and x86-64 each pass all 13 QOI groups.
The raw records retain exact source, tool and input hashes under the checkpoint identifier image-qoi-packed-pixels-20261107.

All 451 native and 464 actual Wasm image cases pass.
All 282 Python controls pass.
Source checks, strict module complexity, type checks and exact formatter checks pass.
The compiler and runtime sources remain unchanged.
