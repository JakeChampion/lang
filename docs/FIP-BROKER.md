# Bounded message ownership and recycling

The broker recycles preallocated message records without allocation in its unique
state, including fan-out to sixteen subscribers. An external message alias kept
alive past acknowledgement causes one allocation when that slot is reused.
Queue-full refusal allocates nothing. These are measured properties of this
single-threaded experiment, not guarantees about a network service.

This implements the bounded broker experiment in #9589, part of #9582.

## Lifecycle and capacities

Construction chooses 1..16 topics, 1..16 subscribers, 1..4096 message slots and
1..4096 queued deliveries per subscriber. Subscriber `s` receives topic
`s % topics`. Each subscriber may also hold one active lease. Queue capacity
counts queued deliveries; pool capacity counts distinct messages, including a
producer reservation and messages held only by consumer leases.

The lifecycle is explicit:

1. `reserve` removes a free slot and moves its record into the producer field.
   It consumes the record while replacing topic, serial and payload.
2. `publish` checks every destination queue before changing any of them. A full
   queue leaves the producer reservation available for retry or cancellation.
3. Successful publication moves the record into broker storage and puts its
   scalar slot index in each destination FIFO.
4. `consume` moves a delivery out of the queue and retains an immutable message
   reference in that subscriber's lease.
5. `acknowledge` checks the message serial, releases the lease and returns the
   slot to the free pool only after the last queued or leased delivery is gone.

`cancel` returns an unpublished reservation. Invalid topic/subscriber indices,
empty queues, occupied leases, absent producer reservations and stale/duplicate
acknowledgements are refused explicitly. A topic with no subscribers is refused.
Serials advance on reservation, including canceled reservations. Reaching the
maximum i64 serial prevents another reservation; it never wraps.

Operations return the owned broker with status/result fields. Status 0 is success;
1 bad argument; 2 producer busy; 3 pool full; 4 queue full; 5 empty queue;
6 invalid lifecycle; 7 stale acknowledgement; 8 serial exhausted; 9 no subscriber.
Constructor refusal precedes allocation. The example is not a general-purpose
library: its record fields are visible, and callers must preserve its invariants.

## What ownership removes, and what remains

The free pool holds actual `Message` records. Reserving a slot detaches that
record from its array before consuming its donor. Consumer leases retain actual
references, so fan-out tests sharing rather than merely copying integer handles.
Payloads remain immutable until all deliveries are acknowledged. The compiler's
reference counts then permit reuse when the record is unique, or preserve an
external snapshot by copying when necessary.

`own` removes manual allocation/free calls and payload copying from this path.
It does not decide the application lifecycle: the example still maintains free
indices, ring heads/lengths, phases, delivery counts and acknowledgement serials.
Delivery counts are distinct from runtime reference counts. The former decide
when the broker may logically recycle a slot; the latter protect external aliases.
That bookkeeping is a substantial part of this implementation and should remain
visible when assessing whether the API feels natural.

Borrowing accessors make record extraction explicit. Separating queue insertion
from publication's record transfer allowed the strict FIP verifier to accept the
implementation; the combined function was rejected with E068. This records an
ergonomics limitation, not a complete diagnosis of the compiler's original refusal.
Callers also read an acknowledgement serial before passing the broker as an owned
argument, avoiding a later read of the same consumed argument.

## Correctness and contracts

An independent Go model uses slice FIFOs and derives delivery counts from queued
and leased messages. After every operation, tests compare every message value,
phase, delivery count, valid free-stack entry, queue position, lease, serial,
status and result. Deterministic scenarios and seeded operations cover saturation,
asymmetric fan-out, retry/cancel, queue wrap, pool reuse and invalid operations.

Pinned tests cover full queues with empty peers, a pool exhausted by active
leases, minimum/maximum capacities, signed payload limits, serial exhaustion,
whole-broker snapshots, individual message aliases and rejected fresh allocation
under FIP/FBIP. Unique allocation checks begin at the first operation after
construction. x86-64 Linux, ARM64 Linux and WASI pass with strict IR, sanitizer
and balanced allocation census. The driver additionally checks arguments, FIFO
serials/payloads and independently verified output checksums/percentiles.

## Measurement method

Measurements used macOS 15.8 arm64 and the native primary compiler from
`4e6f4d29c16920487c4ca4fa4372b58b176834cf`, SHA256
`24043bc6354b7f60d8fdd493af8753412bb7e27e162dddebb18a7833beb25a9b`.
The experiment was staged on merged main `b6ab8c2b0`; its compiler, stdlib and
official bootstrap pin are unchanged from that compiler revision. Executable
SHA256: `ff5df570d65e0f411cdf7c2a33fec59551cd97f6d64a294531e7e0a3d447e332`.

The small five-turn pipeline passed in 0.929 seconds. The expanded capacity pilot
passed in 0.922 seconds, the same configurations at 1000 turns in 1.146 seconds,
and five repetitions in 3.229 seconds. Only the scale or repetition parameter
changed at each step. The repeated set contains 300 runs: three subscriber counts,
five occupancies, two modes, two sharing controls and five repetitions.

Flow mode fills the queues first, then each turn consumes, validates and
acknowledges the oldest message for every subscriber before publishing one
replacement. Occupancy stays fixed. Full mode uses one additional producer slot
and repeatedly attempts publication into full queues. These are refusals, not
delivered messages. In this scenario, the first destination is already full,
so refusal timing does not measure a scan through all subscribers.

The unique control reads the oldest payload before the turn. The shared control
holds that message across acknowledgement/recycling and reads it afterward.
Both validate the same payload, with no full-broker snapshot in this benchmark.
Timing storage is preallocated. Turn samples include delivery validation; outer
wall time also includes alias checks and sample storage. Fill allocation counts
are separate. Export and sorting occur after the marks. The independent harness
checks closed-form serial-stream sums and every reported percentile.

## Turnover results

Rows summarize five runs of 1000 turns. Throughput is thousands of completed
message turnovers per second, median and full range, using the instrumented wall
interval. Multiply by subscribers for delivered messages per second. Latency
columns are medians of each run's reported percentiles. At 1000 samples, p99.9
is the second-largest observation; these are not service-level tail guarantees.
Timer quantization is visible, especially for short refusals.

| Subscribers | Occupancy | Sharing | K turnovers/s median [range] | p50 / p95 / p99 / p99.9 ns |
| ---: | ---: | --- | ---: | ---: |
| 1 | 1 | unique | 7334.981 [1744.059, 7599.708] | 125 / 167 / 167 / 209 |
| 1 | 1 | shared | 7023.705 [1671.659, 7323.754] | 125 / 167 / 167 / 250 |
| 1 | 8 | unique | 7375.502 [7241.969, 7749.415] | 125 / 167 / 167 / 208 |
| 1 | 8 | shared | 7025.778 [6910.468, 7288.205] | 125 / 167 / 167 / 209 |
| 1 | 64 | unique | 7268.340 [7140.766, 7660.370] | 125 / 167 / 167 / 208 |
| 1 | 64 | shared | 7054.674 [6863.041, 7290.383] | 125 / 167 / 167 / 209 |
| 1 | 512 | unique | 7255.157 [7241.969, 7448.790] | 125 / 167 / 167 / 208 |
| 1 | 512 | shared | 6950.478 [6874.789, 7270.506] | 125 / 167 / 167 / 209 |
| 1 | 4095 | unique | 7279.345 [7060.900, 7549.506] | 125 / 167 / 167 / 208 |
| 1 | 4095 | shared | 7058.807 [6655.574, 7290.383] | 125 / 167 / 167 / 209 |
| 4 | 1 | unique | 3357.586 [3223.643, 3435.446] | 292 / 333 / 334 / 375 |
| 4 | 1 | shared | 3259.548 [3035.279, 3397.986] | 292 / 334 / 334 / 375 |
| 4 | 8 | unique | 3328.252 [3278.689, 3442.827] | 292 / 333 / 334 / 375 |
| 4 | 8 | shared | 3188.521 [3151.671, 3362.283] | 292 / 334 / 334 / 375 |
| 4 | 64 | unique | 3335.179 [3288.122, 3433.972] | 292 / 333 / 334 / 375 |
| 4 | 64 | shared | 3204.707 [3169.984, 3380.754] | 292 / 334 / 334 / 375 |
| 4 | 512 | unique | 3333.333 [3187.678, 3455.234] | 292 / 333 / 334 / 334 |
| 4 | 512 | shared | 3241.932 [3162.055, 3399.915] | 292 / 334 / 334 / 375 |
| 4 | 4095 | unique | 3394.629 [3320.428, 3486.349] | 292 / 292 / 334 / 375 |
| 4 | 4095 | shared | 3363.232 [3237.996, 3377.431] | 292 / 333 / 334 / 375 |
| 16 | 1 | unique | 1104.820 [1080.497, 1121.548] | 875 / 917 / 959 / 1000 |
| 16 | 1 | shared | 1066.667 [932.727, 1122.282] | 917 / 959 / 1167 / 1292 |
| 16 | 8 | unique | 1071.811 [1055.966, 1133.734] | 917 / 959 / 959 / 1000 |
| 16 | 8 | shared | 1088.435 [1050.099, 1125.176] | 916 / 958 / 1000 / 1084 |
| 16 | 64 | unique | 1086.858 [1047.304, 1113.999] | 916 / 958 / 1042 / 1250 |
| 16 | 64 | shared | 1050.282 [1021.016, 1117.892] | 958 / 959 / 1000 / 1292 |
| 16 | 512 | unique | 1093.344 [1078.749, 1127.290] | 916 / 917 / 959 / 1084 |
| 16 | 512 | shared | 1073.874 [1053.741, 1124.175] | 917 / 959 / 959 / 1000 |
| 16 | 4095 | unique | 1103.296 [1063.924, 1128.191] | 875 / 959 / 1125 / 1250 |
| 16 | 4095 | shared | 1111.524 [1066.903, 1120.815] | 875 / 917 / 958 / 1000 |

All fills and unique measured turns allocated zero times. Shared flow allocated
exactly once per turn, including the first, across every subscriber count and
occupancy. Its fresh heap growth was either 0 or 56 bytes over 1000 turns because
storage was recycled; this is high-water growth, not total requested or copied
bytes. No unique or refusal run had fresh heap growth.

Unique flow throughput medians were roughly stable across these occupancies,
while larger fan-out increased work per turnover. Some shared medians exceed
unique medians, and run ranges overlap. The extra allocation does not establish
a universal throughput penalty from this small experiment. No hardware cache
counters were collected and no locality speedup is claimed.

## Full-queue refusal results

The following rates are millions of refused publish attempts per second, not
message throughput. Each row gives unique/shared medians and ranges, followed
by their median p99 latency. Full percentile and process data remain in the
machine-readable results. Both controls allocated zero times.

| Subscribers | Occupancy | Unique M refusals/s [range] | Shared M refusals/s [range] | Unique / shared p99 ns |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 1 | 33.241 [29.304, 34.783] | 34.682 [32.787, 34.783] | 42 / 42 |
| 1 | 8 | 33.287 [32.787, 34.833] | 33.380 [29.740, 34.883] | 42 / 42 |
| 1 | 64 | 34.091 [33.287, 34.833] | 33.947 [33.287, 34.883] | 42 / 42 |
| 1 | 512 | 33.380 [32.922, 34.885] | 33.333 [32.876, 34.783] | 42 / 42 |
| 1 | 4095 | 33.333 [32.922, 34.783] | 33.241 [32.832, 34.783] | 42 / 42 |
| 4 | 1 | 32.877 [30.227, 33.333] | 33.288 [33.287, 34.833] | 42 / 42 |
| 4 | 8 | 33.426 [32.831, 34.783] | 32.967 [32.564, 34.682] | 42 / 42 |
| 4 | 64 | 33.333 [32.876, 34.783] | 33.287 [32.967, 34.140] | 42 / 42 |
| 4 | 512 | 33.380 [32.876, 34.783] | 33.333 [32.787, 34.832] | 42 / 42 |
| 4 | 4095 | 34.682 [32.876, 34.883] | 34.783 [32.697, 34.883] | 42 / 42 |
| 16 | 1 | 34.043 [5.041, 34.833] | 34.091 [32.787, 34.732] | 42 / 42 |
| 16 | 8 | 33.058 [32.876, 34.833] | 32.922 [26.756, 34.833] | 42 / 42 |
| 16 | 64 | 34.139 [29.814, 34.885] | 34.583 [33.104, 34.885] | 42 / 42 |
| 16 | 512 | 33.288 [32.787, 34.832] | 33.380 [32.787, 34.833] | 42 / 42 |
| 16 | 4095 | 34.237 [33.103, 34.783] | 34.632 [32.967, 34.885] | 42 / 42 |

## Process resources and limits

Across all runs, construction plus queue fill ranged 1,084..953,000 ns.
Whole-process user+system CPU ranged 0.003718..0.013902 seconds, with peak RSS
1,146,880..2,179,072 bytes. Major-page-fault outliers reached 25. These metrics
include startup, fill, output export and sorting; page faults are not cache misses.

The experiment uses one producer, one topic for benchmarks, fixed subscribers
and a single thread. Correctness also covers multiple topics. There is no network,
concurrent scheduling, durability, variable-sized payload or dynamic subscription
work here. The scalar payload keeps the experiment focused on record ownership;
it does not establish the cost of copying large message bodies. Full-queue timing
does not model recovery from a slow consumer. The independent lifecycle tests
cover refusal/retry and asymmetric progress separately.

## Reproduction

```sh
uv run scripts/bench-broker.py --compiler /path/to/native/fern --output /tmp/broker-pilot
uv run scripts/bench-broker.py --compiler /path/to/native/fern \
  --subscribers 1 4 16 --occupancies 1 8 64 512 4095 --turns 5 --output /tmp/broker-capacity
uv run scripts/bench-broker.py --compiler /path/to/native/fern \
  --subscribers 1 4 16 --occupancies 1 8 64 512 4095 --turns 1000 --output /tmp/broker-scale
uv run scripts/bench-broker.py --compiler /path/to/native/fern \
  --subscribers 1 4 16 --occupancies 1 8 64 512 4095 --turns 1000 --repeats 5 --output /tmp/broker-repeated
```

Compact results, metadata and oracle values are in
[`benchmarks/broker-2026-10-07`](benchmarks/broker-2026-10-07/metadata.json).
Full ordered samples, source snapshots and raw outputs remain at
`/tmp/fern-broker-mainb6-repeated/`. The harness reproduces that evidence layout
and records source/compiler hashes without pretending staged sources were merged.
