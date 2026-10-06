# Byte-array cells

**Status:** Implemented and validated
**Context(s):** Compiler, standard library
**Date:** 2026-10-04

## Problem

Streaming HTTP needs shared mutable byte buffers. Cells currently admit
scalars and strings, so private stream state stores arbitrary bytes in
strings. This violates the valid UTF-8 string invariant even when the
public stream returns bytes.

## Solution

Admit Cell[u8[]]. An owned byte array cannot refer to another value or
close a reference cycle. Cell reads return immutable value snapshots;
updates preserve existing aliases and reclaim replaced storage.

## User Stories

1. A stream implementation can retain arbitrary binary data across pulls.
2. A caller can keep a snapshot while another alias replaces the cell.
3. Long-lived programs reclaim overwritten buffers and the final cell.

## Implementation Decisions

- Extend the cell element rule only to one-dimensional owned byte arrays.
- Reuse typed IR cell ownership and array reclamation on native and WASM.
- The reference compiler must preserve the same ownership contract.
- Evaluate and retain replacements before releasing the old value.
- Keep the compiler source buildable by the pinned bootstrap compiler.
  Its interpreter may encode byte-cell storage as valid text internally;
  compiled application cells store byte arrays directly.
- Migrate HTTP pending input, chunk decoder state and pipelined leftovers.

## Testing Strategy

Test public construction, get and set with all byte values, empty arrays,
self-assignment, retained snapshots, closure and container aliases, repeated
overwrites and final drop. Check rejected cycle-capable types and borrowed
views. Run allocation censuses, both interpreter paths, native and WASM
targets, per-module compilation and bootstrap reproduction. Exercise binary
HTTP bodies and pipelined leftovers through the stream API. Measure copying
and allocation behavior before making performance claims.

## Out of Scope

General reference cells, composite element types, borrowed cell elements,
concurrency primitives and changes to HTTP framing or suspension semantics.

## Open Questions

None. Target, ownership, integrated bootstrap reproduction and Darwin validation
passed with the string-layer completion. The later scalar-array specification
extends this element rule while retaining these byte-buffer contracts.
