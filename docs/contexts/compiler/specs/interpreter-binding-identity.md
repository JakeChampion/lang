# Interpreter closure binding identity

**Status:** Implemented; integration CI required before merge
**Context(s):** Differential interpreter oracle, lexical environments
**Date:** 2026-09-09

## Problem

A closure retained an environment whose name membership could change after
creation. A later declaration redirected an earlier capture, producing 99
instead of 7 in the self-hosted lexical-scope migration regression. An escaping
array capture also lost its owning reference when its parameter scope ended.

## Solution

Capture stable binding identities visible at declaration. Assignment updates
the same binding; a declaration creates a distinct binding. Preserve captured
aggregate ownership after scope exit so aliases cannot observe in-place writes.

## User Stories

1. As a Fern developer, I expect a closure to retain its original lexical
   bindings while observing permitted assignments to those bindings.
2. As a compiler contributor, I need an independent, correct interpreter oracle
   when replacing self-hosted capture and ownership analyses.

## Implementation Decisions

- Promote captured bindings to shared cells; ordinary locals remain direct
  values. Reuse the environment table for both, with cell unwrapping confined
  to binding operations and the COW verifier.
- Use checked capture lists when available. Parser-only evaluation snapshots
  visible membership, never an environment that gains later declarations.
- Register a local function's own binding before capturing it for recursion.
- Captured cells retain ownership across scope exit. As with interpreter
  containers, Go GC owns lifetime and COW counts remain conservative.
- Preserve borrowed map parameter behavior until a capture promotes the
  parameter to an owning binding. Array parameters already own their reference.

## Testing Strategy

Use table-driven language tests and direct environment invariants for later
declarations, writes, nesting, local-function recursion, escaping mutable
scalars and array aliases. Compare independently pinned oracle results with
x86-64, ARM64 and Wasm. Run the full interpreter suite in ordinary and COW
verification modes. Measure native oracle runtime, allocations and binary size.

## Out of Scope

New language syntax, new Go compiler optimization infrastructure, precise
closure destruction in the GC interpreter, and claims of completed self-hosted
typed-IR production cutover or AST ownership retirement.

## Open Questions

None for this oracle repair. Self-hosted capture integration continues after
the independently pinned differential oracle is correct.
