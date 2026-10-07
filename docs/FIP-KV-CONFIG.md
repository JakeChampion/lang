# Startup-configured key/value experiment

This successor completes the startup configuration missing from the original
key/value experiment. Entry capacity, key width, value width and maximum batch
size are runtime arguments. The historical `kv_*` programs remain unchanged.
All three representations implement the same byte protocol: a bounded FIP table,
an ordinary Map and a persistent PMap. The map controls allocate their natural
storage and response arrays; they do not allocate an unused bounded table.

Integration with merged main `5072b424038e9d714355520e1edbccd8520014c4`
passed all required-target KV tests, repository lint and a byte-identical native
bootstrap fixed point. A fresh native pilot produced the same KV executable as
the measured main546 version. The measurements retain their original labels.

Across these measurements the unique bounded table made zero allocations from
its first batch and had the lowest median wall time in all 54 configuration/mix/
load groups. Sharing changes the result: PMap had the lower median in one group,
and Map in two groups. Those three ranges overlap. This is evidence for these
programs and workloads, not a general ranking of collection types.

## Semantics and capacity

`new_db(capacity, key_bytes, value_bytes, batch)` accepts 1..4096 entries,
1..64 key bytes, 8..256 value bytes and 1..1024 requests. Invalid configuration
returns None before allocating subject storage. Configuration belongs to each
database, so differently sized instances coexist.

Requests contain an operation byte, fixed-width key and fixed-width value.
PUT inserts or replaces, GET returns a value, DELETE removes a key, and INCREMENT
adds the first eight little-endian value bytes modulo 2^64 while preserving any
suffix. At capacity, replacing an existing key succeeds and inserting a new key
returns FULL. GET/DELETE/INCREMENT on missing keys return MISSING. Each batch is
validated before changes: bad operations, truncated input, invalid offsets or
excessive counts preserve the entries and previous output atomically.

The bounded table preallocates power-of-two slots, key/value storage and response
bytes. It uses linear probing and backward-shift deletion. Its processing contract
consumes owned database state and borrows the request tape. Shared immutable
snapshots remain valid; they can require copying and are not allocation-free.

## Correctness and measurement boundaries

Primary-compiler tests cover x86 Linux, ARM Linux and WASI with strict IR,
sanitizer and balanced allocation census. An independent Go dictionary checks
every response and logical entry, collisions, wrapped deletion, full capacity,
increment wrapping, configuration isolation and snapshots. Pinned tests cover
maximum dimensions, malformed-batch atomicity and rejected allocation contracts.
Driver tests compare full exported dictionaries, snapshot/response hashes and
every percentile, including a complete cyclic tape and wraparound. Invalid CLI
arguments and a one-byte key universe larger than 256 are refused.

The native harness uses a separate Python bytes-key dictionary. Each run must
match all final entries and checksums and every percentile calculated from the
ordered raw samples. It records compiler, binary and source hashes, including
the stdlib, exact commands, CPU, RSS and page faults. The bounded unique variant
must report zero measured allocations. Full tests and repository lint passed;
full current-head CI and merged acceptance remain separate publication gates.

Startup includes request generation, sample storage, database construction and
prefill. Prefill allocation is also reported separately. No measured warm-up is
discarded. Each latency sample covers `process` alone. Wall time and allocation
marks also include a borrowed old-state probe, response digest and sample
bookkeeping. In unique mode the probe runs before processing; shared mode keeps
the complete old database alive across processing and probes it afterward.
Both modes do the same extra traversal. Export and sorting occur after marks.

Whole-process CPU/RSS/faults include startup, sample export and sorting. Fresh
bytes measure allocator high-water growth, not total requested bytes. Page
faults are not hardware cache misses. No cache-locality conclusion is measured.

## Workload and reproducibility

Native measurements use macOS 15.8 ARM64 and the primary compiler built from main
`546920c8a7bc75e056f4e5ad494fec50ac770188`, official stage0 pin
`stage0-20261006-0d42e82`. Stages 2/3 were byte-identical. Compiler SHA-256:
`e0d9b05b3f2bee6c892dd815b20eb4c4870cd0eb7df73c98a79757dd91d6350e`.
The core source is the PR11861 revision `b90d17b4f`; driver and harness hashes
are in each [metadata file](benchmarks/kv-config-2026-10-07/base/metadata.json).

The tape contains 100 batches. Its deterministic operation cycle is mixed
(50% GET, 25% PUT, 15% INCREMENT, 10% DELETE), read-heavy (80/10/5/5) or
write-heavy (20/45/20/15). Complete cycles preserve these proportions even at
batch size one. The initial native pilot revealed that a shorter tape could omit
writes at small batches; that tape was replaced before collecting these results.
The executable's generator and independent oracles specify every key/value byte.

The load argument is the eligible key-domain size as a percentage of capacity:
50, 100 or 150. It is not a promise of that occupancy. Prefill uses half of the
smaller of the domain and capacity. The oracle records actual post-batch
occupancy and response statuses. Larger domains exercise FULL refusal.
Batch-size changes also change tape length and grouping; they are whole-workload
comparisons, not a fixed-tape batching-only experiment.

A 54-run, five-turn pilot passed in 0.639 seconds of summed native process time.
The scale ladder changed turns to 1000, capacity to 64, key width to 8, value
width to 32 and batch size to 16 one dimension at a time. Five repeats followed.
Additional configurations each change one base dimension: capacity512, key32,
value128, batch1 or batch64. Each had a full pilot before five repeats. In total
there are 1620 repeated runs, each with 1000 ordered batch samples. Representation
order rotates between repeats; configuration order remains fixed. There is no
confidence-interval or long-duration stability claim.

Run the harness, for example:

```sh
uv run scripts/bench-kv-config.py --compiler /path/to/native/fern \
  --capacity 64 --key-bytes 8 --value-bytes 32 --batch 16 \
  --turns 1000 --repeats 5 --output /tmp/kv-config-results
```

## Base workload results

Wall nanoseconds per request, median (minimum-maximum) across five runs, unique
state. These intervals include the probe/digest/bookkeeping described above.

| Mix | Key domain % | Bounded FIP | Map | PMap |
|---|---:|---:|---:|---:|
| mixed | 50 | 334.9 (330.0-376.3) | 380.0 (374.7-395.5) | 416.1 (406.7-418.8) |
| mixed | 100 | 319.5 (312.5-332.3) | 368.2 (345.2-395.6) | 412.5 (393.3-418.8) |
| mixed | 150 | 252.7 (240.6-257.4) | 303.7 (290.8-319.5) | 322.8 (298.0-348.2) |
| read | 50 | 283.2 (277.4-309.8) | 355.9 (350.9-378.0) | 378.7 (362.5-389.4) |
| read | 100 | 249.5 (233.5-250.7) | 323.2 (306.4-325.2) | 330.8 (325.3-345.0) |
| read | 150 | 255.8 (252.3-261.7) | 334.3 (310.4-337.1) | 327.9 (319.6-347.1) |
| write | 50 | 252.4 (242.9-255.4) | 292.3 (287.0-303.8) | 340.5 (333.5-347.5) |
| write | 100 | 243.7 (243.4-245.8) | 296.8 (289.8-303.5) | 340.4 (327.1-349.8) |
| write | 150 | 176.8 (175.1-179.2) | 233.2 (224.2-239.1) | 252.1 (246.3-256.2) |

For the base mixed workload, actual average occupancy was 28.417, 54.719 and
61.988 of 64 entries for eligible-domain percentages 50, 100 and 150. The last
produced 594 FULL responses among 16000 requests. Read-heavy and write-heavy
occupancies differ; every exact profile is in the corresponding oracle JSON.

The mixed/domain100 base case shows the sharing cost and measured tails. Latency
columns are median per-run percentiles of 16-request batches, in nanoseconds.
Allocations and fresh bytes cover all 1000 measured batches.

| Mode | Representation | Wall ns/request (range) | p50 | p95 | p99 | p99.9 | Allocations | Fresh bytes |
|---|---|---:|---:|---:|---:|---:|---:|---:|
| unique | fip | 319.5 (312.5-332.3) | 2125 | 2417 | 2833 | 2958 | 0 | 0 |
| unique | map | 368.2 (345.2-395.6) | 2917 | 3083 | 3750 | 4167 | 74822 | 5480 |
| unique | pmap | 412.5 (393.3-418.8) | 3542 | 3792 | 4417 | 4625 | 84450 | 6832 |
| shared | fip | 340.2 (323.3-353.2) | 2458 | 2750 | 2875 | 3417 | 5824 | 8952 |
| shared | map | 396.7 (374.3-437.6) | 3167 | 3417 | 4167 | 4375 | 77822 | 11368 |
| shared | pmap | 417.3 (394.9-442.9) | 3500 | 3792 | 4042 | 4750 | 101010 | 11544 |

## Where sharing changes the ranking

The following groups had a lower ordinary/persistent median than the bounded
table. Their ranges overlap, so these runs do not establish a stable advantage.
Keeping the result avoids treating zero-allocation unique behavior as a universal
performance claim. Units remain wall nanoseconds per request.

| Configuration | Mix/domain % | Bounded FIP shared | Map shared | PMap shared |
|---|---|---:|---:|---:|
| capacity512 | mixed/50 | 409.6 (401.6-625.6) | 424.3 (421.7-494.1) | 400.5 (377.7-431.1) |
| value128 | mixed/100 | 1202.2 (1192.0-1205.3) | 1199.0 (1167.3-1200.8) | 1221.9 (1212.3-1295.2) |
| value128 | write/50 | 939.7 (923.1-958.0) | 931.2 (922.2-942.7) | 1003.8 (965.0-1105.1) |

## Startup and process costs

Base mixed/domain100 medians. CPU and RSS cover the whole process, so they cannot
be attributed solely to the measured processing loop. RSS is bytes; CPU is
user plus system seconds; startup is microseconds. The full ranges, first-batch
allocation counts, prefill allocation and fault counts are retained in data.

| Mode | Representation | Startup us | Startup allocations | Process CPU s | Peak RSS bytes |
|---|---|---:|---:|---:|---:|
| unique | fip | 145.875 | 244 | 0.008999 | 1490944 |
| unique | map | 147.000 | 364 | 0.009653 | 1507328 |
| unique | pmap | 143.500 | 462 | 0.010641 | 1490944 |
| shared | fip | 141.041 | 244 | 0.009204 | 1490944 |
| shared | map | 156.583 | 364 | 0.010424 | 1523712 |
| shared | pmap | 143.125 | 462 | 0.010894 | 1507328 |

## Interpretation and retained evidence

The bounded representation benefits from fixed widths, known maximum capacity
and preallocated output. Map/PMap offer ordinary collection interfaces and pay
for key/value conversion and response construction in these implementations.
This experiment compares complete equivalent applications; it does not isolate
the map algorithm or show that these are optimal Map/PMap implementations.

Unique ownership supports reuse, but retaining a complete prior database forces
copies. Persistent sharing can sometimes offset the bounded table's copying
cost. The measured negative groups and overlapping ranges constrain the result.
The two controls are not forced to allocate beyond their normal implementation.
The public processing API borrows request data and consumes database replacement,
allowing callers to keep input tapes while choosing whether to retain old state.

The [full summary](benchmarks/kv-config-2026-10-07/summary.json) contains every
metric's median and range. Each configuration directory contains all run reports,
independent oracle profiles, source/compiler hashes and compile commands:

- [base results](benchmarks/kv-config-2026-10-07/base/results.jsonl), [oracle](benchmarks/kv-config-2026-10-07/base/oracle.json), [metadata](benchmarks/kv-config-2026-10-07/base/metadata.json).
- [capacity512 results](benchmarks/kv-config-2026-10-07/capacity512/results.jsonl), [oracle](benchmarks/kv-config-2026-10-07/capacity512/oracle.json), [metadata](benchmarks/kv-config-2026-10-07/capacity512/metadata.json).
- [key32 results](benchmarks/kv-config-2026-10-07/key32/results.jsonl), [oracle](benchmarks/kv-config-2026-10-07/key32/oracle.json), [metadata](benchmarks/kv-config-2026-10-07/key32/metadata.json).
- [value128 results](benchmarks/kv-config-2026-10-07/value128/results.jsonl), [oracle](benchmarks/kv-config-2026-10-07/value128/oracle.json), [metadata](benchmarks/kv-config-2026-10-07/value128/metadata.json).
- [batch1 results](benchmarks/kv-config-2026-10-07/batch1/results.jsonl), [oracle](benchmarks/kv-config-2026-10-07/batch1/oracle.json), [metadata](benchmarks/kv-config-2026-10-07/batch1/metadata.json).
- [batch64 results](benchmarks/kv-config-2026-10-07/batch64/results.jsonl), [oracle](benchmarks/kv-config-2026-10-07/batch64/oracle.json), [metadata](benchmarks/kv-config-2026-10-07/batch64/metadata.json).

Raw ordered stdout/stderr and complete source snapshots are retained locally at
`/tmp/fern-kv-config-repeated-base` and the five
`/tmp/fern-kv-config-{capacity,key,value,batch1,batch64}-repeated` directories.
The committed compact data does not include every raw sample; the harness can
reproduce and preserve them using a supplied native primary compiler.
