# Bounded storage and page-cache simulator

**Status:** Implemented and locally validated; pending publication and merged acceptance
**Contexts:** Runtime ownership, bounded operation state, deterministic simulation
**Date:** 2026-10-07

## Problem

Storage operations need retained state while completion is delayed. Authors need
to know whether preallocated owned state machines can replace per-operation heap
tasks while preserving bounds, errors and immutable snapshots.

## Solution

Build an in-memory fake disk and bounded page cache with read, write, flush and
evict operations. Separate allocating configuration from strict state transitions.
Make queue pressure, dirty eviction, delays, conflicts and injected errors explicit.

## User stories

1. A caller submits operations within a fixed outstanding bound and retrieves each
   completion exactly once, without losing results when polling is slow.
2. A caller observes cached writes immediately and durable fake-disk changes only
   after successful flush, with failed operations preserving prior data.
3. An author compares native processing cost at increasing queue depth while
   distinguishing logical completion delay from host execution time.

## Implementation decisions

A configured simulator owns preallocated disk/cache data, request slots,
completion slots and metadata. Operation slots move from free to queued to
scheduled to completed, then become free when the matching ticket is collected.
Unpolled completions remain outstanding. Tickets and ticks never wrap.

Bounded round-robin advances make progress deterministic. Scheduled operations
pin the target page and cache frame; conflicts return explicit results. Clean
unpinned frames may be replaced deterministically; dirty frames require flush
before eviction. Injected errors occur before committing data changes. All
subject updates consume owned state; retained snapshots remain immutable.

## Testing strategy

Compare full state and completions against an independent model. Pin durability,
replacement, saturation, delayed/error completion, stale polling, bounded work,
capacity/overflow and alias behavior on required primary-compiler targets.
Assert zero allocation from the first unique data-plane operation. Measure a
small native pipeline before queue-depth scaling and retain raw evidence.

## Out of scope

Physical storage, filesystems, networking, concurrency, crash recovery, a general
storage library and claims of physical-I/O performance.

## Open questions

No product decision is missing. Tests pin scheduling conventions, faults and
ownership boundaries. The experiment report records measured queue-depth,
allocation and snapshot costs, with physical I/O explicitly out of scope.
