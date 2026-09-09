# Resolved record ownership contracts

**Status:** In progress
**Context(s):** Self-hosted compiler
**Date:** 2026-09-09

## Problem

Record copies can contain borrowed children while owning a fresh record box.
Classifying a source variable once cannot describe both values. The existing
semantic SSA pipeline represents distinct values and counted container stores,
but lacks resolved nominal record schemas.

## Solution

Represent nominal construction and field projection in the existing typed
semantic graph. Verify exact instantiated type identity, ordered field names
and types, then derive dependencies and counted stores from those operations.
Borrowed fields acquire independent units when stored in a new record. The
new record owns a unit independently of the source binding's classification.

## User Stories

1. As a Fern developer, I need a copied record to preserve the original's data.
2. As a compiler maintainer, I need invalid or incomplete schemas rejected
   before ownership planning, including ambiguous generic instances.
3. As a compiler maintainer, I need independent replay to reject missing field
   supplies, invalid moves and leaked fresh replacement records.

## Implementation Decisions

- Extend semantic SSA and the existing counted-unit planner and verifier.
  Do not introduce another ownership graph or infer facts from physical RC.
- A schema identifies one exact resolved nominal instance and its ordered,
  uniquely named fields. Duplicate instance schemas are invalid. Nested record
  field types must resolve; recursive schema references are finite handles.
- Construction supplies operands in schema order. Projection includes the
  expected field name and index, both checked against the schema.
- Abstract reference counting admits verified records and their field types.
  Physical lowering remains closed until record ABI validation and recursive
  drops are implemented and independently tested.

## Testing Strategy

Extend the existing semantic and counted-unit mutation corpus. Run identical
fixtures with the bootstrap compiler and self-hosted compiler across native
and Wasm targets. Verify borrowed field anchors, exact generic identity,
duplicate stores and fresh replacement cleanup. Assert that the current
physical boundary refuses a valid record plan with no output operations.
Physical follow-up must add executed shared/unique record cases, heap churn,
exact native allocation balance and missing-retain negative controls.

## Out of Scope

This contract slice does not claim to fix production record copying, remove
AST ownership, implement nominal physical layouts, or authorize destructive
reuse. Production cutover requires physical record drops and verified call
contracts. Parsing and initial type checking remain frontend responsibilities.

## Open Questions

Recursive physical drops need finite generated helpers, not unbounded compiler
recursion. Resolve that representation before admitting recursive record
schemas to physical lowering.

## Validation checkpoint

On base main `36ca99210`, the 47-case semantic corpus passes under the Go-built
driver and the actual self-host CLI for ARM64, x86-64 and Wasm. The extended
34-case counted-unit corpus passes through the same compiler/target matrix,
including borrowed and counted replacements, repeated fields, empty records,
recursive nominal schemas and record loop phis. Mutated plans must reject
missing supplies, duplicate moves, leaked replacement roots and changed
schemas. A valid record construction plan is rejected by physical lowering
with no operations or locals.

The initial semantic/unit/rejection run passed in 136.921 seconds; the final
expanded unit run passed in 58.217 seconds. Repository lint and the unchanged
complexity ratchet pass. The existing executable physical-RC matrix also
passes under both bootstrap and self-host-built drivers in 154.995 seconds,
including all native/Wasm targets, x86 sanitizer, exact native heap balance
and deliberately omitted-retain failures. The linked compiler measures 11,602,460 bytes and
passes the existing strict size gate; this local report covers one of fifteen
drivers, not the complete CI size matrix. No baseline is changed and no speed
claim is made. Linux ARM64 runs use the local devbox image, with QEMU for x86
and Wasmtime for Wasm. These are correctness observations.

Main's generation-one x86 function-count and ARM64 module-needs checks still
exit 134 in CI. Their stages are consistent with the #8989 investigation,
but the fresh CI logs alone do not establish the same underlying cause.
Full integration CI and the production ownership repair remain outstanding.
