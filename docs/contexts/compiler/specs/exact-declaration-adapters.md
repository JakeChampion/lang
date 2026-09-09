# Exact declaration adapters

**Status:** Implementation
**Context(s):** Self-hosted semantic types and callable lowering interfaces
**Date:** 2026-09-09

## Problem

Typed captures and pattern bindings need to preserve full callable signatures at
the current lowering boundary. The production lowerer reparses callable spelling
independently of the structural type parser and rejects grouping around a
function type. Losing the contract risks incorrect calling-convention metadata.

## Solution

Integrate the exact declaration adapters from the preserved typed-match work.
Use the existing structural type parser for source declarations and the shared
semantic Type for checked declarations, with one signature metadata constructor.

## User Stories

1. As a Fern developer, I want grouping and array syntax to preserve the intended
   callable signature through checking and native/Wasm lowering.
2. As a compiler contributor, I want checked capture and binding types to reach
   existing declaration interfaces without losing widths, views or nested types.
3. As a compiler contributor, I want unresolved signatures explicitly declined
   rather than silently assigned a default calling convention.

## Implementation Decisions

- Keep the shared semantic type as the authority for checked values.
- Reuse the structural parser to distinguish callable values from callable arrays.
- Share declaration metadata construction between production source adapters
  and future typed binding/capture consumers; delete the replaced hand parser.
- Preserve the current Type variants and declaration schema. Do not activate
  incomplete typed-match syntax or add ownership analysis at this boundary.
- Capture the complete structural callable contract at every declaration site,
  deleting independent parameter/dyn token peeks and their selector. Keep named
  parameter syntax out of semantic types and preserve grouping/array attachment.
- Carry the checker's known callable binding type through the shared adapter.
  Reuse the type already resolved for scope advancement instead of checking the
  initializer twice; annotate its expressions in the preceding lexical scope.
  Attach the contract directly when its local slot is created in any lexical
  block, removing the top-level signature pre-pass and string-keyed seed table.
- The existing capture declaration lookup must consume the complete retained
  contract rather than dropping it down to a result-only string. This does not
  replace lexical capture resolution or claim typed-capture/ownership cutover.
- Callable return values must obey the same closure-object ABI as parameters
  and fields. Boxing a named function at a callable return boundary is
  independent of its arity; already-bound callable values remain unchanged.

## Testing Strategy

- Reproduce the production adapter failure with valid grouped signatures and
  rejecting non-callable controls before changing it.
- Integrate the preserved exact-signature tests, including recursive Types,
  concrete widths, views, dyn positions and explicit unknowns.
- Test actual callable execution through the production CLI on supported targets
  with independent expected behavior and bootstrap/interpreter parity.
- Run targeted tests, lint, source checks and full CI; measure code-size changes
  against the exact parent with an identical compiler and linker.

## Out of Scope

New syntax, TypeNever, view-parameter schema expansion, new ownership heuristics,
unrelated backend improvements, and claiming AST ownership retirement.

## Open Questions

Verify all existing signature consumers agree with the structural parser before
switching production callers. Any demonstrated mismatch needs a real fix and
regression, not a permissive fallback.
