# Bounded ring shared by application experiments

**Status:** Prototype, application correctness and native comparisons validated locally; awaiting publication
**Context:** Runtime ownership and application examples
**Date:** 2026-10-07

## Problem

The event loop and message broker implement their own fixed-capacity FIFO
bookkeeping. Both require wraparound, explicit overload and immutable snapshots.
Their duplicated queue logic is a concrete reason to test a shared abstraction.

## Solution

Prototype one generic bounded ring local to the examples and use it in explicit
successor variants of both applications. Preserve measured inline versions as
controls and measure any allocation, copying or throughput cost of the abstraction.

## User stories

1. An author initializes capacity once and receives explicit full/empty refusal.
2. An author preserves a queue snapshot without corrupting queued values.
3. An author compares a shared queue with the existing application representation.

## Implementation decisions

The control plane validates capacity before constructing fixed backing storage.
The data plane consumes the ring on push/drop and borrows readers. A scalar status
and caller-supplied fallback avoid fresh result containers. Removed entries are
cleared to an explicit sentinel. There is no automatic growth. Payload construction
remains a caller boundary; queue reuse does not guarantee unique payloads.

The event loop keeps ordered events; the broker keeps pool indices and its
atomic fanout/lease protocol. Storage completion slots remain separate because
completion order is not FIFO. This prototype creates no standard-library API.

## Testing strategy

Use an independent FIFO model on all required targets, including wrap, full,
empty, capacities at their limits, retained snapshots and allocation contracts.
Check both application protocols against their existing references. Benchmark
equivalent work under unique/shared inputs against inline rings and ordinary
arrays, with a small native pilot before scaling. Report negative findings.

## Out of scope

Speculative collection families, networking changes and performance claims
without measurements.

## Open questions

Generic queue operations and both application integrations pass required-target
tests with zero allocations for unique state. Explicit generic record literals
and a borrowed nested-queue accessor are needed by the current compiler.
The native queue and broker comparisons now record throughput, tails and copying
costs in [FIP-RING](../../../FIP-RING.md). The generic broker is slower than its
inline control; it remains an explicit successor rather than replacing that control.
Event-loop integration is correctness/allocation validated, with no throughput
claim and its linear response drain disclosed. Publication and merged CI remain.
