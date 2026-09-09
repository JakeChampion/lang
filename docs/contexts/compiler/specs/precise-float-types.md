# Precise semantic float types

**Status:** Implementation
**Context(s):** Self-hosted compiler semantic types and typed-match integration
**Date:** 2026-09-09

## Problem

The production self-hosted checker cannot distinguish concrete float widths or
an uncommitted float literal. Typed match results and captures cannot safely
carry those distinctions into semantic IR if the shared type has erased them.
The Go compiler already enforces concrete-width compatibility.

## Solution

Integrate the precise float representation from the existing typed-match work.
Preserve concrete width and literal polymorphism through checking, contextual
settlement, diagnostics and annotation. Keep initial checking in the frontend.

## User Stories

1. As a Fern developer, I want concrete float-width mismatches diagnosed while
   literals can adopt the width required by their context.
2. As a compiler contributor, I want typed joins and captures to carry the
   checked float type without reconstructing it from syntax or a default tag.
3. As a developer, I want the Fern-written compiler to agree with the bootstrap
   compiler on diagnostics and generated behavior across supported targets.

## Implementation Decisions

- Extend the existing shared float variant with width and literal-polymorphism
  fields. Preserve the variant order and avoid a second semantic type model.
- Distinguish category membership from width compatibility. Use the same
  compatibility rule for expression inference and coded diagnostics.
- Preserve existing literal contextual typing and explicit casts. Do not turn
  concrete-width mismatches into implicit conversions.
- Integrate the preserved implementation and its tests. Audit semantic labels,
  callable arguments, arithmetic, comparisons and annotation consumers together.
- Keep ownership analysis, calling conventions and target instruction selection
  unchanged. This is a prerequisite, not a claim of AST ownership retirement.
- Carry concrete float width through checked lowering annotations. The shared
  lowering classifier must select single-precision rounding for checked call
  results and projections without reconstructing their type from syntax.
- Join literal and concrete branch results recursively and independently of arm
  order. A preceding literal cannot conceal conflicting concrete widths later.
- Thread branch-local declarations into terminal-value checking. Pass the full
  scrutinee type into pattern binding and return diagnostics, retaining builtin
  generic payload arguments and whole-value bindings. Reuse this contract in
  annotation rather than maintaining a separate unknown-payload path.

## Testing Strategy

- Reproduce current diagnostic gaps with the actual Fern checker and Go oracle.
- Inspect recursive semantic types and literal-versus-concrete distinctions.
- Test valid contextual literals and invalid concrete mixes across assignments,
  calls, returns, arithmetic, comparisons and receiver dispatch.
- Execute representative programs through the production compiler on x86-64,
  ARM64 and Wasm; include independent expected values, not fixpoints alone.
- Run existing checker/differential, schema, source-lint and full CI gates.
- Measure generated size and attribute costs before changing any baseline.

## Out of Scope

New syntax, new numeric conversions, TypeNever, view-parameter metadata, AST
ownership heuristics, unrelated optimizations, and activating incomplete match
lowering. Full typed-match integration follows these semantic prerequisites.

## Open Questions

No product decision remains. Any downstream assumption that all floats are f64
must be investigated and corrected at its proper boundary, not bypassed.
