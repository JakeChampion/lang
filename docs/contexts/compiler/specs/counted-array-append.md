# Counted array append and payload-return contracts

**Status:** Implemented; integration validation in progress
**Context(s):** Self-hosted lowering, ownership and runtime
**Date:** 2026-09-09

## Problem

The caller transfers a counted scalar-array reference to a consuming parameter.
Expression append can return a different buffer, but return cleanup treats its
old receiver as transferred to the result. The old reference is then stranded.
Several enum census expectations also assume that returning a borrowed payload
can give away its parent's reference. A caller retaining the parent disproves
that assumption by reading corrupted data after allocator churn.

## Solution

Make append's result and input reference obligations agree with the existing
consuming-call protocol. Preserve counted payload returns and test their actual
parent/child lifetime, rather than using a historical leak count as a safety
oracle.

## User Stories

1. A caller may retain a snapshot while passing another counted array reference
   to a consuming function, without corruption or a stranded input reference.
2. A borrowed enum may remain live after a returned payload is released.
3. A conditional payload return transfers its reference on the return edge and
   releases it on the fallthrough edge.

## Implementation Decisions

- Reuse declared scalar-array ownership, existing append borrowing decisions
  and physical IR reference operations. Add no AST escape or last-use analysis.
- Reuse the parent repair's settlement of an unbracketed consuming append
  through the existing array-store contract. An identical result transfers its
  original reference; a copied result replaces and releases the old input.
- A bracketed append returns a separate counted buffer; retain the original
  parameter's normal exit-cleanup obligation.
- Apply the same reference protocol to narrow, wide and floating array elements.
- Keep a borrowed payload's counted return distinct from its parent's reference.
  Root reclamation remains an explicit typed-IR migration requirement; a
  non-borrowable verdict is not proof that a parent has no surviving readers.

## Testing Strategy

Use the existing field-lift leak gate and all three self-hosted target runners.
Add shared-input and unique/growing append cases across element widths, a
receiver also read by the appended operand, and conditional payload returns
under allocator churn. Require exact balance on the repaired array paths.
Pin a live-parent outcome independently and check that removing its return
retain corrupts the result. The two existing enum-root census failures remain
unmodified merge blockers. Full non-Netlify CI and review gates still apply
before merge.

## Out of Scope

New AST ownership heuristics, pointer-element array ownership changes, or
claiming that conservative enum-root reclamation is complete.

## Open Questions

The typed pre-RC migration must carry parent/child containment and counted
escape effects so the old conservative enum-root release refusal can be
retired without guessing ownership from freshness or uniqueness.
