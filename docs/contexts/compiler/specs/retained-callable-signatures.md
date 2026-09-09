# Retained callable signatures

**Status:** Implementation
**Context(s):** Self-hosted compiler semantic types and checking
**Date:** 2026-09-09

## Problem

The self-hosted compiler retains structured function declarations but discards
their parameter types when resolving them into semantic types. Consumers cannot
distinguish a known empty parameter list from an unrecorded one. This prevents
typed captures and later ownership analysis from relying on callable contracts,
and can suppress argument checking on function values.

## Solution

Resolve retained function signatures into the existing shared semantic callable
type, preserving ordered parameter types, the result and known arity. An opaque
legacy function tag remains explicitly unknown rather than becoming zero-arity.

## User Stories

1. As a Fern developer, I want calls through retained function types checked
   against their declared arguments, including genuine zero-argument functions.
2. As a compiler contributor, I want typed captures and semantic IR to consume
   the same structured callable contract as the checker without reconstructing it.
3. As a developer using generic or unresolved types, I want missing information
   kept explicit rather than replaced with invented types or arity.

## Implementation Decisions

- Extend the structured type resolver, not the physical backend or AST ownership
  classifier. Reuse the shared callable type and existing signature constructors.
- Resolve ordered parameters and the result recursively in the same nominal
  context. Reuse scalar-only grouping normalization from structured type decoding.
- Known arity and known constituent types are distinct: an unresolved parameter
  remains unresolved even when its position and the parameter count are known.
- Keep an opaque legacy function tag opaque. Do not change calling conventions,
  ownership effects or the production float model in this change.
- Integrate the resolver design already present in unfinished typed-match work.
  This is a prerequisite for production typed IR, not ownership retirement itself.
- Consume retained scalar and array callable declarations consistently across
  locals, parameters, fields and returned functions. Preserve array-parameter
  sidecars at parsing, and substitute enclosing type variables within arrows.
- Diagnose value calls using the resolved callable contract, including direct
  projections and returned values. Preserve existing literal-range, borrowing
  and composite-array argument rules without duplicating free-function diagnostics.
- Resolve nested dynamic trait types at the recursive type boundary, using the
  same semantics as a top-level dynamic trait annotation.
- Struct and enum payload fields share one declaration-type parser. All payload
  positions, named forms and callable arrays retain complete parameter sidecars.

## Testing Strategy

- Test the structured resolver through its public interface, inspecting complete
  recursive parameter/result types rather than a lossy debug string.
- Include zero-arity versus opaque signatures, nested functions, grouping,
  arrays, tuples, nominal generics and unknown constituents.
- Check valid and invalid calls against the Go compiler as a semantic oracle;
  ensure argument diagnostics do not regress valid function-value programs.
- Run production function/call/CLI and Wasm suites, whole-source lint and CI
  self-host fixpoints. Fixpoints supplement independent behavioral tests.
- Measure compiler size and compile cost. No external service mocks are needed.

## Out of Scope

Float-width improvements, new syntax, backend changes, ownership inference,
new callable ABI rules, and claiming completion of typed-IR or AST retirement.

## Open Questions

No product decision remains. Existing consumer behavior under newly known
signatures must be verified before publication; mismatches require investigation,
not bypasses or weaker expectations.
