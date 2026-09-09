# Checked iterator bindings

**Status:** Implemented, full PR validation pending
**Context:** Self-hosted checking and lexical capture contracts
**Date:** 2026-09-09

## Problem

Loop checking and capture annotation disagreed about the names and types bound
by map entries and tuple-array patterns. Missing key/value bindings could also
resolve to a shadowed outer declaration, silently recording the wrong type.
The existing map-capture runtime regressions on #8982 expose this gap (#8991).

## Contract

The iterator type defines the loop body's bindings. Ranges bind an integer;
arrays bind their element type; map pairs bind their key and value types.
Tuple patterns use the shared recursive destructuring rule. Discards introduce
no binding, and loop-local declarations do not escape into the enclosing scope.

Checking, capture annotation and scope-aware diagnostics consume the same
binding operation. Closure conversion consumes the checked capture metadata;
it must not reconstruct binder types by searching surrounding syntax.

## Validation

Four added capture-metadata tests fail before the repair and pass afterward,
including nested tuple patterns, wide values and shadowed map keys. Metadata
must preserve its type and declaration identity through lexical resolution.
Fourteen loop programs run on each of x86-64, ARM64 and Wasm, checked against
the interpreter. Diagnostic tests compare both accepted and rejected programs
with the bootstrap checker, including return, argument and assignment types,
discard reads, and restoration of outer scope.

## Boundary

This repairs source-level binding semantics before typed IR construction. It
adds no AST ownership heuristic and does not claim to resolve the separate
counted-record ownership or whole-compiler self-compilation failures.
