# FIP application architecture experiments

**Status:** In progress
**Contexts:** Runtime allocation, ownership verification, standard library
**Date:** 2026-10-06

## Problem

Application authors need evidence about where ordinary allocation, bounded
reuse and strict allocation contracts improve real programs. Small kernels
alone do not establish whole-application ergonomics or tail latency.

## Solution

Complete the remaining application experiments and compare equivalent work
under each memory discipline. Publish reproducible measurements and a guide
that explains capacities, ownership, sharing and overload behavior.

## User stories

1. An application author can compare allocating and bounded telemetry pipelines.
2. An author can see whether a retained snapshot changes reuse and allocation.
3. An operator can see what happens when configured capacity is exceeded.
4. A compiler contributor can reproduce rejected contracts and missing APIs.

## Implementation decisions

Start with telemetry ETL: deterministic fixed-width input, explicit validation
and normalization, quality filtering, counter/min/max/histogram aggregation,
and bounded encoding. Compare per-record allocation with ordinary batching,
FBIP batches and FIP batches. Keep the algorithm and logical input identical.
Reuse the existing benchmark observables and distinguish allocation count
from fresh heap bytes. Allocate measurement storage before the steady-state mark.

Keep application-specific structures local until multiple experiments establish
a shared need. Preserve immutable snapshots and runtime uniqueness checks.
Compiler or library defects receive coherent fixes before dependent experiments
are presented as validated.

## Testing strategy

Use an independent reference for output and aggregate values, with empty,
partial, full and excessive batches, malformed records and retained snapshots.
Run through the primary compiler on native Darwin, Linux and WASI. Assert
strict steady-state allocation counts and reject accidental allocating calls.
Validate a small measurement pipeline before scaling; capture native timing,
latency distributions, memory, capacities and raw results without concurrent
heavy work. QEMU supplies correctness evidence only.

## Out of scope

The networking epic, speculative collection APIs, annotation quotas, and
performance or cache claims inferred solely from source shape.

## Open questions

No user decision is required. Measurements determine the useful memory
discipline and whether any bounded collections deserve extraction.
