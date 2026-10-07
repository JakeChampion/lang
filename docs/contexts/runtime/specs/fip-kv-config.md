# Startup-configured key/value state

**Status:** Core controls published; driver, target tests and native measurements prepared
**Contexts:** Application examples, ownership and allocation
**Date:** 2026-10-07

## Problem

The original key/value experiment exposes workload controls but fixes storage
dimensions in source. It cannot test the startup configuration requested by
the original issue or two instances with different capacities in one process.

## Solution

Add an explicit successor whose initialization validates entry capacity,
key/value widths and maximum batch size. Preserve the historical programs.
Compare a bounded table with ordinary and persistent map representations under
the same request and response rules.

## User stories

1. An author can construct differently sized databases without recompilation.
2. A caller receives an atomic refusal for malformed or excessive batches.
3. An author can observe the cost of retaining a prior database value while
   preserving its immutable contents.

## Implementation decisions

Use byte records and fixed-width keys/values, with a little-endian wrapping
64-bit increment in the first eight value bytes. Initialization owns allocation;
strict processing uses preallocated storage and bounded table probes. Keep
configuration in each state, not global variables. Ordinary and persistent
comparators share the same semantics but retain their natural storage choices.

The three modules expose `new_db(capacity, key_bytes, value_bytes, batch)` and
`process(database, input, offset, count)`. Limits are 1..4096 entries, 1..64 key
bytes, 8..256 value bytes and 1..1024 requests per batch. The bounded core stores
response length separately; ordinary Map and PMap controls build response arrays.
All reject an invalid batch before changing entries or the previous output.
Invalid configuration returns `None` before allocation. The core PR contains no
benchmark claim. Its successor adds `kv_config.fern`, deterministic runtime
workloads and an independent measurement harness. See [the measured report](../../../FIP-KV-CONFIG.md).

The driver uses a 100-batch prebuilt tape, mixed/read-heavy/write-heavy operation
cycles and explicit unique/shared whole-database controls. Input construction,
prefill and sample storage are charged to startup. Subject samples cover batch
processing; wall/allocation intervals additionally include borrowed snapshot
checks and response digests. Export and sorting occur after those intervals.
Eligible key-domain percentages 50/100/150 vary load; the independent oracle
records actual occupancy and FULL responses rather than equating them to the
requested percentages. One-byte keys refuse a domain larger than 256.

Required-target driver tests compare complete logical dictionaries, response and
snapshot digests, every percentile and tape wraparound. Native pilots preceded
single-dimension scaling and 1620 repeated runs across six configurations. Unique
bounded processing allocated nothing in every run; shared states allocate and
three measured shared groups favor an ordinary/persistent median with overlapping
ranges. The report preserves that limitation and exact compiler/source labels.

## Testing strategy

Independent dictionary and wire oracles compare every response and final value.
Test configuration limits, batch atomicity, collisions, full-table updates,
deletion, increment boundaries and retained snapshots through the primary
compiler on all required targets. Verify zero allocations for unique strict
processing and refusal before allocation for invalid configuration. Benchmark
small cases before scaling, with exact compiler provenance and equivalent work.

## Out of scope

Changing historical benchmark sources, networking, production persistence,
new map APIs and a claim that bounded storage is always faster.

## Open questions

Performance and any compiler ergonomics findings require measurement. No user
decision is needed to complete the original configuration requirement.
