# Building bounded applications with FIP and FBIP

**Status:** In progress
**Contexts:** Runtime allocation, ownership verification, application examples
**Date:** 2026-10-07

## Problem

The application experiments use several compiler generations and ownership
representations. Readers need to distinguish current contracts from historical
defects, and reusable design choices from timing results on one host. An
allocation-free implementation can still copy shared inputs, scan too much
state, or lose to an ordinary implementation.

## Solution

An application guide connects the verified contracts to initialization,
bounded transitions, overload handling and measurement. Each recommendation
points to an implemented experiment. Measurements retain their compiler and
workload provenance. Earlier reports remain historical evidence.

## User stories

1. An author can choose ordinary, FIP or FBIP code using measured behavior and
   the contract each choice enforces.
2. An author can locate state, buffers and allocating boundaries before adding
   annotations, and preserve snapshots without assuming they are free.
3. An operator can distinguish queue refusal, completion latency and processing
   time, and understand what the benchmark does not establish.
4. A contributor can reproduce a reported result and trace a claim to code,
   target tests, negative contract tests and raw evidence.

## Implementation decisions

The guide is a consumer of the compiler contracts and application reports; it
introduces no new runtime or collection API. Prefer links to tested source over
new illustrative programs that could drift. Explain the runtime uniqueness
fallback independently of static reuse pairing. Preserve the distinctions
between allocation handoffs, fresh high-water growth, copying and process RSS.

The final evidence matrix covers the closed event-loop, packet, key/value,
in-memory request and benchmark-helper experiments as well as ETL, simulation,
DSP, broker, storage and the shared collection successor. A historical result
does not become a current benchmark by linking it from a new document.

## Testing strategy

Check every source and report link and every numerical claim against its cited
evidence. Use existing positive and negative contract tests as the source of
language claims. Compile any newly introduced runnable example with the primary
compiler and verify its semantics across required targets. Documentation-only
edits need link, whitespace and repository lint checks; they do not justify
repeating passing application benchmarks. Run a small pilot before any new
measurement, then change one scale parameter at a time.

## Out of scope

Networking implementation, new compiler semantics, speculative collection
families, real-time or hardware-cache guarantees, and silently updating the
provenance of historical results.

## Open questions

The shared-ring successor and configurable KV benchmark are merged and linked.
The original startup-configuration gap is implemented and measured. A fresh
mainf939 report records 3,130 runs of all five applications, ring comparisons
and configurable KV after their executables changed. It preserves historical
main11a/main546 evidence and the negative performance findings. The guide
and recorded measurements are ready for review on the merged application code.
Full merged CI and the original epic acceptance audit remain closure requirements.
Neither requires a new user decision.
