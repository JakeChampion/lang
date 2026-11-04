# Worker result reclamation

**Status:** Accepted design, implementation in progress
**Context(s):** Runtime, worker boundary, compiler backends
**Date:** 2026-11-04

## Problem

Each completed native Linux worker retains its shared region after join.
Repeated joins retain one GiB of virtual address space per worker.
Balanced reference counts do not prove that the runtime removed these regions.

## Solution

Join copies the result into the parent heap and then removes the worker region.
The returned value remains valid after later joins and allocations.
The user selected this design on 2026-11-04.

## User Stories

1. A program can join repeated workers without permanent growth in worker regions.
2. A program can retain nested results across later joins without invalid pointers.
3. A refused fork releases its unused region before the inline fallback runs.
4. A failed worker releases its region before the parent reports the failure.

## Implementation Decisions

- The standard worker module owns the copy and reclamation sequence.
- Both native Linux backends remove the region through a small unsafe runtime primitive.
- The primitive reports a kernel failure. Join must not return after a failed removal.
- The parent consumes the original result without a recursive release into its free lists.
- A successful region removal reclaims every live allocation in that region.
- The leak census records this physical reclamation only after a successful kernel operation.
- A failed fork or worker does not import incomplete child counters.
- Worker reference counts remain non-atomic. Each process owns its heap.
- Both checkers retain send safety and the requirement to consume each worker.
- Darwin, Wasm, and both interpreters retain their inline fallback.
- Programs without workers retain their allocator behavior.

## Testing Strategy

Use public worker operations to test repeated joins and nested result lifetimes.
Measure virtual memory on native Linux before and after four joins, then thirty-two joins.
Only the join count changes between these two measurements.
Run repeated joins on both native Linux targets with the leak census active.
Test nested strings, arrays, records, and enums across later joins and parent allocations.
Test the kernel refusal paths at the runtime boundary.

Retain the deep-copy, heap-adoption, send-safety, and inline worker controls.
Run the compiler source checks, diagnostic checks, bootstrap fixed point, and conformance census.
Do not change a baseline to hide a failure.

## Out of Scope

Native threads, shared mutable heaps, atomic reference counts, and worker execution order changes.
This change does not complete the separate security acceptance issues.

## Open Questions

None. The user approved the copy-and-unmap design and the regression boundaries.
