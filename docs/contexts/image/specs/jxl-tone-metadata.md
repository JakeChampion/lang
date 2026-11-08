# JPEG XL tone metadata

**Status:** Locally qualified. Publication remains subject to source and PR gates.
**Context:** Fern-native image engine.
**Date:** 2026-11-08.

## Scope

Add the complete internal tone metadata descriptor in pure Fern.
Preserve the default form and explicit intensity, minimum luminance and linear threshold.
Preserve the flag that makes the threshold relative to display luminance.
This stage describes metadata. It does not convert pixels or select a display policy.
Full JPEG XL pixel and container support remains required by the parent spec.

## Contracts

Borrow input bytes and honor a closed logical extent.
Use the qualified finite F16 reader and writer.
Return exact values and the next position.

Validate positive intensity, minimum luminance bounds and the relative threshold domain.
Reject malformed metadata without access beyond the logical extent.
Write the compact default form when all values match their defaults.
Preserve exact output budgets through the qualified raw writer.

## Research gate

Compile the original tone descriptor class and bundle reader and writer.
Measure admission, exact bytes and decoded values before runtime implementation.
Probe signed zeros, tiny positive intensity, finite boundaries and every field relation.
Check whether writer admission guarantees that its own reader accepts the result.
Record any original-code discrepancy before a stricter safety rule.

The original release class passes 8,192 independent requests against the reference model.
It accepts 1,500 requests. Its own reader rejects 140 of those outputs.
Their tiny positive intensity truncates to zero, which violates the descriptor's positive-intensity rule.
The Fern writer must reject that case before output.

The original default visitor uses an absolute tolerance of one millionth for float fields.
It can replace representable nonzero minima with zero and bypass small negative inputs.
The Fern writer validates semantic bounds before default selection.
It uses the compact form only for exact defaults after the finite metadata conversion.
Explicit fields preserve all other finite values under the qualified F16 rules.

The original reader accepts both forms. No pixel conversion policy changes.

## Validation

Use original class execution, independent field equations and real image fixtures.
Cover the compact and explicit forms, bit offsets, every cut and output budgets.
Run a small complete pipeline before the full corpus.
Run native, actual Wasm and applicable Linux checks with exact source evidence.
Keep reference libraries and original C++ code outside the runtime.

The complete corpus covers every half word in each scalar position and both flag modes.
It checks original writer results and independently verifies every admitted Fern output with the original reader.
Five real codestreams supply complete tone descriptors and original basic information.
The routine CI lane uses frozen fixtures and fifty-five pinned original source files.
Linux GCC retains two frame-header constants during its original writer optimization.
The reference build includes their original translation unit and signed-value helper.

## Public decisions

No new public image API or sample conversion policy applies here.
The confirmed wide-pixel model and the unresolved byte-helper policies remain separate.
