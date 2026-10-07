# Bounded deterministic simulation

**Status:** In progress
**Contexts:** Runtime allocation, immutable collections, ownership verification
**Date:** 2026-10-06

## Problem

Application authors need to know what persistent collections, owned record
arrays and bounded arrays cost in a complete simulation tick. A checksum alone
can hide incorrect phase ordering or proximity searches.

## Solution

Implement equivalent deterministic integer simulations in three representations.
Each tick handles input, steering, movement, proximity, state updates and a
checksum. Compare native timing and allocation while validating every state field
against an independent reference.

## User stories

1. An author can compare representations without changing simulation rules.
2. An operator can identify capacity refusal and worst-case tick work.
3. A compiler contributor can reproduce allocation and snapshot behavior.

## Implementation decisions

The world is a fixed-size integer torus. Entities carry position, velocity,
health, kind, optional target and idle/chase/recover state. Phase boundaries
prevent earlier entity updates from changing what later entities observe.
A uniform spatial grid narrows proximity candidates; a clustered population
still has quadratic worst-case work, bounded by the configured entity limit.

The persistent variant uses the existing persistent vector. The FBIP variant
uses an owned array of records, and the FIP variant uses bounded arrays for
individual fields. Scalar rules are shared; representation-specific loops make
allocation costs visible. No networking or shared collection API changes.

## Testing strategy

An independent all-pairs reference checks the grid result and every state field.
Exercise empty and single-entity worlds, wrap boundaries, absent targets,
health transitions, dense clusters and retained snapshots. Require primary
compiler target parity, truthful allocation contracts and meaningful refusal
tests. Pilot measurements before scaling, keep raw samples and report the
cost of measurement separately where observable.

## Out of scope

Rendering, networking, floating-point physics, unbounded entity growth and
generalizing collections before multiple experiments establish a need.

## Open questions

No product decision is missing. Measurements determine which representation
fits this workload and which compiler or library restrictions need follow-up.
