# Scalar-array cells

**Status:** Implemented; local validation passed, full CI pending
**Context(s):** Compiler, runtime
**Date:** 2026-10-05

## Problem

Byte-array cells support shared binary buffers, but cells containing other
scalar arrays are still rejected. These arrays cannot hold a reference to a
cell, so they satisfy the same cycle-free ownership rule. Issue #11448 asks
for scalar-array support beyond the completed byte-array slice.

## Solution

Allow cells containing one-dimensional owned arrays of integers, floating-point
values or booleans. Reading a cell returns an immutable array snapshot. A later
replacement or mutation through another cell alias must preserve that snapshot.
Replacing a payload and dropping the final cell alias reclaim their storage.

## User Stories

1. A program can share a replaceable numeric or boolean buffer through a cell.
2. A caller can retain a snapshot while another holder replaces the buffer.
3. An empty buffer and repeated overwrites behave consistently on every target.
4. Interpreted execution preserves integer widths, unsigned values and floating
   bit patterns just as compiled execution does.

## Implementation Decisions

- Extend the existing cell element rule to arrays whose element is a supported
  integer, f32, f64 or boolean. Retain the existing scalar and string cell rules.
- Keep nested arrays, strings as array elements, records, variants, functions,
  cells and borrowed views outside this extension. They are not scalar arrays.
- Both checkers enforce the same rule for annotations and inferred construction.
- Use typed IR's existing counted-array ownership for cell construction, reads,
  replacement and final drop. Evaluate replacements before releasing the old
  payload, including self-assignment and updates derived from a cell read.
- Preserve buildability with the pinned bootstrap compiler. Interpreter-private
  storage may encode scalar bits as valid text, but must preserve element type
  and value exactly. Compiled cells continue to hold arrays directly.
- Keep the existing byte-array HTTP consumers and their interfaces unchanged.

## Testing Strategy

Test every scalar representation through construction, get and set. Cover
empty arrays, retained input and output aliases, self-assignment, replacement
derived from a read, repeated overwrites, container and closure aliases, and
final reclamation. Use boundary integers and exact floating-bit comparisons,
including signed zero, infinities and NaNs. Reject unsupported reference-bearing
elements and borrowed views through both annotated and inferred calls.

Run a small compiled and interpreted pilot before broad validation. Then check
both interpreter paths, native and WASM targets, per-module compilation and
allocation censuses. Bootstrap reproduction must remain byte-identical at the
repository's required generations. Do not mock the compiler or ownership runtime.

## Out of Scope

General reference cells, recursive cycle analysis, multidimensional cells,
concurrency primitives, HTTP behavior changes and performance guarantees for
interpreter-private encoding.

## Open Questions

None in the public contract. The scalar matrix, floating-bit edge cases,
checker differential, per-module execution, both interpreter paths and Apple
Silicon execution pass. Allocation censuses balance on the native and WASM
targets. Native bootstrap stages 1, 2 and 3 are byte-identical. Affected checker,
IR and interpreter unit suites and full lint pass. Full CI and PR review remain
required before merging.
