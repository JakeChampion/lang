# Partial enum constructor inference

**Status:** Implemented; conversion-parity follow-up locally validated, CI pending
**Context(s):** Compiler
**Date:** 2026-10-06

## Problem

A variant payload can determine some of its enum's type arguments without
determining all of them. The checkers discard the known arguments when another
remains unresolved, so matching an unannotated success or error value loses
the payload's usable type.

## Solution

Keep every argument determined by a constructor payload. Leave other arguments
open for surrounding context to resolve. Matching the value exposes its known
payload type whether the constructor is bound to a local or matched directly.

## User Stories

1. A success value containing an integer supports integer operations in its arm.
2. An error value containing a string supports string operations in its arm.
3. Later annotated context can complete the other type argument.
4. User-defined generic enums follow the same inference rule.

## Implementation Decisions

- Extend both the primary checker and the reference checker used by tooling.
- Preserve known arguments separately from unresolved inference positions.
- Preserve numeric settlement and contextual widening, generic declarations,
  constructor name resolution and the checked metadata supplied to typed IR.
- Unresolved arguments must not fabricate a concrete type or hide a conflict.
- At typed lowering, a genuinely absent payload is an internal uninhabited
  type, distinct from `void`, unknown diagnostics and template parameters.
  It cannot be constructed as a value. Enum schemas retain impossible variants
  so their positions stay stable; only inhabited variants require runtime walks.
- Completing a partial enum rebuilds its sole inhabited variant using
  verified projection and construction. A permitted lossless integer widening
  uses an explicit typed cast; aliases retain the original value and type.
- Declared enum variants are instantiated from owner-qualified typed schemas.
  Each generic instance has distinct physical construction, match and cleanup
  names; nongeneric declarations retain their existing identities.
- Frontend clones retain their original instantiated spelling, rewritten with
  the declaring module's prefix. Generic declarations remain available as
  templates. Contextual completion requires the same nominal origin and
  compatible known arguments and payloads; generated names are not parsed as
  provenance. Explicit integer widths remain fixed. An unsuffixed payload
  carries an inference flag that is settled before typed IR.
- Literal inference classes connect constructors, aliases, match projections
  and closure bodies. The first concrete use settles the original arithmetic
  before evaluation, with range checks and consistent repeated parameters.
- Closure lifting retains absent enum arguments across its declaration
  spelling boundary with an internal `$unbound` token. The source lexer
  rejects it, the resolver recognizes it only as an enum argument, and
  unrelated unknowns remain unresolved. Semantic contracts settle the retained
  argument to the same uninhabited type used by ordinary enum values.
- Inferred return types join known enum arguments from every exit before
  settling arguments that remain absent. Conflicting known payloads still
  produce a diagnostic.
- Direct `Result` construction permits lossless widening between fixed-width
  integers of the same signedness. It rejects narrowing and signedness changes.
  `Option` and already-constructed values retain their stricter rules. Bare and
  qualified constructors preserve the same payload facts. Contextual literal
  range checks descend through constructor payloads, including nested enums;
  ordinary functions and methods with constructor-like names are excluded.

## Testing Strategy

Check local and direct matches for success and error constructors, partial
user-enum inference, contextual completion, numeric widths and conflicting
types. Retain shadowing and qualified-constructor coverage. Compare checker
diagnostics and execute accepted cases through the primary native and WASM
targets. Verify unit tests, lint and bootstrap reproducibility before merge.
No compiler or ownership mocks.

## Out of Scope

New syntax, changes to enum layout, general higher-rank inference and revival
of retired native code generators.

## Boundaries

The public behavior is defined by issue #10165. Contextual completion handles
a single inhabited variant; broader enum conversions and inference from later
reassignment remain separate work. Narrowing and unsigned literal contexts
check ranges, and concrete widths remain fixed.

Issue #11655 verifies conversion parity between the Go oracle and primary
compiler. Its regression corpus checks return, binding and argument contexts,
legal widening, rejected conversions and contextual literal overflow.
