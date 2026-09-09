# Lexical capture contracts

**Status:** Implementation under validation
**Context(s):** Self-hosted frontend and closure conversion
**Date:** 2026-09-09

## Problem

Whole-body name subtraction loses captures before a later shadowing declaration.
Capture type reconstruction repeats frontend reasoning after binding information
has been lost. Tuple bindings can disappear and guarded enum construction can
re-enter the same type query until the compiler exhausts its stack.

## Solution

Resolve free variables in declaration order, record their complete checked
types at definition, and assign distinct internal identities before closure
conversion. Every transformation carries or updates those contracts. Closure
conversion consumes the checked contract instead of reconstructing a type.

## User Stories

1. A closure continues to reference its defining binding when another declaration
   reuses the same spelling.
2. Captures preserve callable signatures, numeric widths, borrowed views and
   unresolved types across direct-call lifting and cell conversion.
3. A contributor can retire duplicate capture reconstruction while retaining
   independent runtime and diagnostic evidence.

## Implementation Decisions

- The frontend owns lexical resolution and initial typing, not ownership planning.
  Shared ordered syntax traversal handles scope boundaries and declaration order.
- Each source declaration receives a per-function identity and collision-free
  internal symbol. Source names and available locations remain in the binding
  catalog. Assignments do not introduce a new declaration.
- Lambda contracts distinguish unchecked syntax from a checked empty capture set.
  Captured bindings carry the shared semantic Type, including unknowns.
- Parameter scopes share declaration adapters. Borrowed string parameters retain
  their view marker across parser, substitution and flattening boundaries.
- Direct-call substitution updates introduced capture types explicitly. Missing
  injected contracts decline the substitution. Cell conversion preserves the
  original semantic element type inside the cell type.
- Assertion elision may remove or reorder captures but cannot invent bindings.
- Raw IR drivers enter the same checked-capture boundary as the CLI. Already
  checked syntax is not reannotated solely for closure conversion.
- Separate compilation shares the complete declaration context before capture
  annotation, including imported nominal types and call results. Each part
  retains its own functions and statements for emission. Callable argument and
  result declarations follow their enclosing parameter through namespace
  rewriting.
- Remove replaced recursive capture type reconstruction. The remaining physical
  closure representation is unchanged; this is not the pre-RC ownership planner.

## Testing Strategy

Pin lexical identities and input immutability. Execute source programs through
the production self-hosted CLI on x86-64, ARM64 and Wasm against independent
expected results and the Go interpreter. Cover nested and mutable captures,
tuple bindings, guarded patterns, wide values and shadowing. Check metadata
through call substitution, cell conversion and assertion elision, including
unknown types and missing contracts. Run existing callable, wide-capture,
mutation, diagnostic and ownership regression suites. Measure compiler and
driver sizes without weakening baselines. Full current-head non-Netlify CI
remains a merge requirement.

## Out of Scope

Adding AST ownership heuristics, changing the physical closure ABI, retiring
native-code targets, or removing the Go bootstrap before self-host parity.
The two enum-root reference census failures require separate counted ownership
integration; this change does not claim to fix them.

## Open Questions

Complete the production typed semantic import of the binding catalog and
replace the remaining ownership queries only after their consumers are verified.

## Validation checkpoint

Thirty scope runtime cases and nine separate-compilation runtime cases pass
on x86-64, ARM64 and Wasm without skips. The broader capture, callable,
value-block and binding-scope run passes in 83.492 seconds; the final runtime
rerun after null-cleanup optimization passes in 46.762 seconds. Lint-all passes.
Imported callable declarations and separate-compilation capture contexts are
covered by regression tests for #8984 and #8985.

All fifteen CI-equivalent linked drivers are measured and smoke-tested against
the callback-repair parent. The whole compiler shrinks from 11,553,004 to
11,540,956 bytes. Partial drivers now include shared checked-capture resolution.
The complete baseline is refreshed after removing avoidable generated cleanup
calls on null, with code-size attribution in SELFHOST-LEXICAL-CAPTURE-SIZE.md.
These are correctness changes, not measured runtime performance improvements.
Full current-head CI and the separate enum ownership repairs remain merge gates.
