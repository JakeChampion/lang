# Bounded message broker

**Status:** Implemented and measured locally; merged acceptance pending
**Contexts:** Runtime ownership, immutable messages, bounded storage
**Date:** 2026-10-07

## Problem

Authors need to know whether owned values make message recycling natural when
queues and fan-out keep messages alive for different durations.

## Solution

Build an offline broker with fixed topics, subscribers, message slots and FIFO
capacities. Make reservation, publication, consumption, acknowledgement and
recycling explicit. Compare unique state with retained message aliases.

## User stories

1. A producer can publish or observe a full queue without partial delivery.
2. A subscriber can hold a message until acknowledgement without losing its value.
3. An author can measure reuse for single-consumer and fan-out workloads.

## Implementation decisions

Subscribers have fixed topic assignments and one active lease each. Publications
preflight every destination queue. A refused publication keeps the producer's
reservation available for retry or cancellation. The last acknowledgement returns
the message slot to the free pool. Monotonic serials prevent stale acknowledgement
from releasing a newer occupant of the same slot. Exhaustion never wraps serials.

Owned broker records contain preallocated messages, queues, free slots and leases.
Consumer leases retain immutable message references. External aliases may force
copying after logical recycle; this is an explicit control, not an exception to
immutable semantics. Application-specific structures stay local to the experiment.

## Testing strategy

An independent queue-and-message model checks lifecycle state, FIFO order,
capacity/refusal behavior and conservation at every step. Test alias retention,
queue wrapping, atomic fan-out, cancellation, pool exhaustion, stale/duplicate ack
and allocation contracts on all required primary-compiler targets. Pilot native
measurements before scaling occupancy, preserving samples and compiler identity.

## Out of scope

Networking, concurrency, persistence, subscriber reconfiguration, unbounded queues
and a general-purpose broker library.

## Open questions

No product decision is missing. The measured unique lifecycle allocates nothing,
including fan-out. An external message alias across recycling costs one allocation
per turnover. Queue/free-slot/delivery/acknowledgement bookkeeping remains explicit.
The report in `docs/FIP-BROKER.md` records measurements and limitations.
