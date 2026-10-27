# Restricted forward ICC model data

The `codecs/icc/matrix_model` package selects owned matrix/TRC data from the current restricted profile envelope.
This slice implements native child #13674 under #13659.
It does not establish complete profile conformance, transform eligibility, destination policy, adaptation, or encoded-image admission.
The existing JFIF and RGB APIs still reject ICC and Exif metadata.

## Request and selection

`read(input, requested, limits, cancelled)` requires an explicit `Relative` request.
It retains the profile header preference separately, without using that preference to select the request.
The existing envelope reader checks v4.4 RGB input/display profiles with XYZ PCS.
An unsupported request fails before callbacks.

Any `A2B0`, `A2B1`, `A2B2`, `D2B0`, `D2B1`, `D2B2`, or `D2B3` tag returns `UnsupportedTag`.
The selector does not silently fall back to matrix data.
This is a conservative project subset, not an ICC prohibition on profiles with several forward models.
Reverse tags and other optional metadata remain opaque to this forward selector.
Their directory metadata remains in `info`, without semantic validation or a retained profile-byte view.

The selector requires `rXYZ`, `gXYZ`, `bXYZ`, `rTRC`, `gTRC`, `bTRC`, `wtpt`, `desc`, and `cprt`.
The current readers check exact XYZ triplets and curve wire shapes.
Description and copyright tags require the v4 `mluc` type, but their text contents remain unparsed.
The optional chromatic-adaptation data remains unparsed, and this selector does not apply it again to the supplied columns.
A successful result therefore does not certify every required or optional profile semantic.

## Owned result and numerical domains

`ForwardData` owns the envelope metadata, explicit request, nine matrix numerators, three curves, and media white point numerators.
The matrix uses row order, with the X coordinates first, then Y, then Z.
Within each row, columns correspond to red, green, and blue.
Signed coefficients remain exact without clipping, positivity assumptions, or conversion to an output color space.

Each curve receives admission through its existing qualified evaluator at a fixed input of zero.
Those evaluators check their full declared coefficient or sample domains before evaluation.
This call does not replace their independent numerical campaigns or authorize wider curve domains.
Identity, positive `u8Fixed8` gamma, sampled curves, and the current bounded parametric subsets retain their existing rules.
An otherwise valid parametric curve outside that numerical subset returns `UnsupportedCurve`.
Undefined admitted-form parameters return `CurveDomain`.

Exact tag aliases and unordered directories remain valid under the existing envelope policy.
Each returned curve contains owned values, including curves from shared tag extents.
Input aliases preserve COW isolation during callbacks.
Returned matrix and curve aliases preserve their snapshots across updates and fresh calls.
No function value is stored inside a cell.

## Limits, errors, and cancellation

`ModelLimits` combines the existing profile and curve limits.
Defaults allow one MiB of profile bytes, 128 tags, one MiB per curve wire element, and 4,096 samples per curve.
The existing hard caps remain 16 MiB of profile bytes, 1,024 tags, one MiB per curve element, and 65,536 samples.
All limits concern logical input and element admission, not allocator capacity or a physical/process bound.
They do not define an aggregate allocator, CPU, or production admission controller.

Nonpositive caller limits return `Limit` before callbacks.
An invalid request then returns `Intent` before callbacks.
The selector preserves distinct envelope and tag errors and distinguishes missing data, unsupported models, metadata type, and curve domain errors.
Every nested cancellation returns the common `Cancelled` error.
Partial metadata and curve results remain private and release on error or cancellation.

Cancellation checks occur in the existing readers, the tag scan, the numerical admission calls, and before success.
Tag and sample loops retain their existing groups of 64 where applicable.
This is cooperative cancellation, not a hard deadline.
A malformed later tag can follow earlier metadata allocation or callbacks.
The API does not promise complete preflight before all work.

## Exact finite reference

The generator constructs profile components, expected integer fields, aliases, physical layouts, and directory permutations independently of the Fern selector.
It declares negative outcomes for explicit wire mutations and project policy restrictions.
The frozen profile supplies a separate known-good metadata observation.
Pinned offline Pillow/LittleCMS properties check its class, spaces, header preference, columns, and media white exactly.
Those host-side properties are metadata observations, not a transform or negative-policy oracle.

The bundle covers signed columns, all current curve forms, limits, missing and mistyped tags, forward-model rejection, opaque reverse metadata, and cancellation.
It also covers 65,536-sample curves, 1,024-tag directories, and nonzero flags, attributes, dates, and profile IDs.
The profile ID remains unverified metadata rather than a validated digest.
No rule for sorting tags, monotonic sampled curves, or additional white-point positivity is invented here.

The private `FMD1` worker packet contains six signed configuration integers and profile bytes.
The worker reports every returned integer, the exact error, and callback counts.
The collector compares all returned integers before it stores an observed-value digest and bounded previews.
The digest identifies actual parsed output values, not only an expected input file.
Each worker must exit successfully with one nonzero balanced heap census, zero live bytes, and no sanitizer finding.

The eight protocol fault groups cover changed interior values, noncanonical numbers, incorrect errors or polls, missing rows, and duplicate rows.
They also reject unbalanced or absent heaps, nonzero exits, sanitizer findings, and malformed reference metadata.
The collector preserves failed batches and validated prefixes and refuses an existing output record.
Its 60-second process timeout limits the experiment and does not establish a runtime API deadline.
The source commit must precede final three-target qualification, with every selected source, tool, reference, and runner hash unchanged.

## Separate future gates

A complete profile model, intent and destination policy, adaptation, transforms, and metadata-bearing JPEG integration require separate qualification.
Optional text and chromatic-adaptation semantics remain open.
Wider curve domains, photographs, quality, performance, security, compatibility, physical/process bounds, and production acceptance remain open.
An external runtime CMM still requires explicit user approval.

## Reference

[ICC.1:2022, profile version 4.4](https://www.color.org/specifications/ICC.1-2022-05.pdf), sections 8.2, 8.3.3, 8.4.3, 8.10, 9.1, and Annex F.3.
The explicit rejection policy and current numerical subset are project decisions.
The public web reader supplied the document, without a local PDF digest claim.

## Completed finite campaign

Source `309dec19100f3ac5ccee69fbccb72cd4ffb8b074` precedes all final records.
Each target checks 401 cases, with 107 accepted data results and 294 expected errors.
Every one of 435,297 returned integers per target agrees exactly with the construction reference.
All three target sample records agree exactly.
All 153 fresh workers have nonzero balanced allocation counts and zero live bytes.

The bundle reproduces exactly with SHA256 `eef63d7b07cd7de8e81bd363d3c1983e97592ca41ed05c81aa7515341bfcc896`.
All 1,274 native and 1,275 runner selected source/tool hashes remain unchanged.
The records are `benchmarks/results/icc-matrix-model-{native,wasm,arm64}.json`.
The phase, source, tool, and cleanup evidence is in `benchmarks/icc-matrix-model-2026-10-27/`.

All 276 TAP cases pass on native, Wasm, and standalone ARM64/QEMU10.
Complete scripts report 78 native, 54 Wasm, and 39 ARM balanced zero-live heap censuses.
Go check and interpreter pass all eight model groups after the source commit.
Both unchanged complexity gates pass without skips, and format/build/vet/lint/protocol/prose checks pass.
These finite observations do not close the separate future gates above.
