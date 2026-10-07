# FIP application measurements on main f93935112

This refresh records 3,130 native runs on merged main
`f939351127f45c4007b2429f965094b29b029c5e`. The five application executables,
five ring-comparison executables and configurable KV executable differ from
their earlier measured versions. Those reports retain their original compiler
labels; the results below are new measurements, not relabeled history.

The ordinary ETL baseline has the highest median throughput at each batch size.
DSP's ordinary direct loop beats the annotated graph at every block-size and
ownership group median. The generic broker queue remains slower in all turnover
groups, although two refusal groups favor it with overlapping ranges. Bounded
KV leads the unique-input comparisons, but three shared-input groups favor a
Map or PMap median. Zero allocation alone does not select the fastest program.

## Provenance and validation

The primary native compiler uses official pin `stage0-20261006-0d42e82`.
Its SHA-256 is
`d503f53ec257a41ef7b5be9ae00b0532c35b078f09dd660005c30b732171b6e9`.
It was compiled from exact main by the documentation-integration compiler,
whose native bootstrap stages 2 and 3 were byte-identical. Recompiling exact
main with the resulting main compiler also produced identical bytes.

All eight harnesses passed small native pilots before larger configurations.
Every measured run passed its independent oracle and ordered-sample checks.
The required x86 Linux, ARM Linux and WASI application suites passed, as did
the staged repository lint checks. This local evidence does not replace full
merged CI or the original issue acceptance audit.

The [manifest](benchmarks/fip-mainf939-2026-10-07/manifest.json) identifies all
15 datasets, their raw directories and the hashes of the copied evidence.
Each directory contains the exact per-run results and metadata; applicable
independent oracle records are included. All recorded source hashes were checked
against measured revision `f939351127f45c4007b2429f965094b29b029c5e`.
Full raw samples and source snapshots remain in the
measurement host's `/tmp/fern-*-mainf939-*` directories. The existing harnesses
reproduce the workloads.

## ETL

Five repetitions of two million records cover four variants and three batch
sizes, for 60 runs. The table reports median throughput and observed minimum
and maximum, not confidence intervals.

| Batch | Variant | Million records/s median (range) | Steady allocations |
|---:|---|---:|---:|
| 1 | baseline | 38.621 (37.025..38.838) | 0 |
| 1 | batched | 24.804 (24.137..24.889) | 4000000 |
| 1 | fbip | 29.487 (23.276..30.032) | 0 |
| 1 | fip | 33.574 (32.683..34.362) | 0 |
| 64 | baseline | 70.330 (69.299..73.001) | 0 |
| 64 | batched | 51.619 (49.362..52.046) | 2156250 |
| 64 | fbip | 55.158 (54.473..56.842) | 0 |
| 64 | fip | 67.301 (66.177..68.708) | 0 |
| 1024 | baseline | 76.262 (69.119..77.005) | 0 |
| 1024 | batched | 54.944 (52.623..55.271) | 2017583 |
| 1024 | fbip | 58.507 (54.797..59.035) | 0 |
| 1024 | fip | 69.569 (67.865..72.431) | 0 |

All variants have zero fresh high-water growth after warm-up. For batch 64,
the medians of each run's batch percentiles are:

| Variant | p50 ns | p95 ns | p99 ns | p99.9 ns |
|---|---:|---:|---:|---:|
| baseline | 750 | 834 | 917 | 1083 |
| batched | 1084 | 1167 | 1416 | 1583 |
| fbip | 1000 | 1084 | 1291 | 1417 |
| fip | 792 | 875 | 1000 | 1166 |

These are batch-processing latencies, not individual-record latencies. At
batch 1, some p50 values are zero and several tails coincide at 42 ns. Such
small measurements cannot establish a tail-latency advantage.
[Complete ETL results](benchmarks/fip-mainf939-2026-10-07/etl/results.jsonl)
retain every run's maximum, startup, process CPU/RSS and faults. The workload
and warm-up rules remain those in [FIP-ETL](FIP-ETL.md).

## Simulation

The 180 runs cover 33, 129 and 513 entities, spread and clustered layouts,
all three representations, both ownership modes and five repetitions of
1,000 ticks. Five-tick capacity pilots also covered 2,048 and 4,096 entities.
The following table selects 129 entities with the spread layout.

| Representation | Ownership | Ticks/s median (range) | Median p99 ns | Allocations/tick |
|---|---|---:|---:|---:|
| persistent | unique | 18753 (17916..18767) | 62875 | 387 |
| persistent | shared | 18426 (17414..18482) | 62458 | 403 |
| fbip | unique | 39684 (38722..39778) | 30083 | 0 |
| fbip | shared | 38510 (38288..38660) | 29541 | 134 |
| fip | unique | 41190 (40542..41267) | 27708 | 0 |
| fip | shared | 40718 (38295..40943) | 27583 | 11 |

The [33-entity](benchmarks/fip-mainf939-2026-10-07/simulation-n33/results.jsonl),
[129-entity](benchmarks/fip-mainf939-2026-10-07/simulation-n129/results.jsonl)
and [513-entity](benchmarks/fip-mainf939-2026-10-07/simulation-n513/results.jsonl)
datasets include the clustered cases and all tails. The grid retains an
N-squared dense worst case. Shared measurements preserve the prior world;
they do not permit mutation of an alias. See [FIP-SIMULATION](FIP-SIMULATION.md).

## DSP, broker and storage

The [120 DSP runs](benchmarks/fip-mainf939-2026-10-07/dsp/results.jsonl) cover
block sizes 1, 7, 64, 256, 1024 and 4096, both implementations and both ownership
modes, with five repetitions of 1,000 blocks. The direct loop wins all twelve
group-median comparisons. Both unique variants allocate nothing, including the
first callback; shared runs allocate four blocks per callback. The annotated
graph's additional work remains a negative result, not an omitted comparison.

The [300 broker runs](benchmarks/fip-mainf939-2026-10-07/broker/results.jsonl)
cover one, four and sixteen subscribers, occupancies 1, 8, 64, 512 and 4095,
turnover and full refusal, both ownership modes and five repetitions of
1,000 turns. Unique turnover and refusal allocate nothing. Shared turnover
allocates once per turn; fresh growth is zero or 56 bytes. This control retains
an external message, not the whole broker.

The [150 storage runs](benchmarks/fip-mainf939-2026-10-07/storage/results.jsonl)
cover depths 1, 3, 8, 32 and 128, all three modeled modes, both ownership cases
and five repetitions of 1,000 waves. Unique execution allocates nothing from
the first wave. Shared execution copies state. Its logical completion ticks
describe the deterministic device model, not actual disk latency. Policies,
faults and work bounds remain documented in [FIP-STORAGE](FIP-STORAGE.md).

## Shared bounded ring

The [400 broker-comparison runs](benchmarks/fip-mainf939-2026-10-07/ring-broker/results.jsonl)
use one or four subscribers, five occupancies, turnover/refusal and both message
ownership cases. Inline queues lead 38 of 40 group medians. The two exceptions
are full refusal at one subscriber/occupancy 64/shared message and four
subscribers/occupancy 512/unique message. Both have overlapping observed ranges.
All twenty turnover comparisons favor inline queues.

The [300 scalar-queue runs](benchmarks/fip-mainf939-2026-10-07/ring-queue/results.jsonl)
cover capacities 1, 8, 64, 512 and 4096, turnover/refusal and unique/shared
whole-queue ownership. Across twenty groups, generic ring leads ten medians,
inline ring eight and the ordinary array two. This count alone does not show
the size or stability of a difference: inspect the per-run ranges and tails.
Unique ring turnover reuses storage; retained queues preserve their contents
through copying. The ordinary array's suffix-copy work remains explicit.
The event-loop integration has correctness and allocation coverage, but its
throughput remains unmeasured and its linear drain is not the original packed
loop's count reset. See [FIP-RING](FIP-RING.md).

## Configurable key/value workloads

There are 1,620 runs across six configurations, three mixes, three eligible-key
domain sizes, three representations, both ownership cases and five repetitions
of 1,000 batches. The base dimensions are capacity 64, key width 8, value width
32 and batch 16. The other configurations vary just capacity to 512, key width
to 32, value width to 128, or batch size to 1 or 64.

Bounded unique execution allocates nothing and leads all 54 unique group
medians. Three shared groups favor another representation: PMap at capacity
512/mixed/50% key domain, and Map at value width 128/mixed/50% and
value width 128/write-heavy/100%. Each overlaps the bounded implementation's
observed range. The load percentage sizes the eligible key domain, not measured
occupancy; the independent oracle records occupancy and FULL responses.

Results: [base](benchmarks/fip-mainf939-2026-10-07/kv-base/results.jsonl),
[capacity](benchmarks/fip-mainf939-2026-10-07/kv-capacity/results.jsonl),
[key width](benchmarks/fip-mainf939-2026-10-07/kv-key/results.jsonl),
[value width](benchmarks/fip-mainf939-2026-10-07/kv-value/results.jsonl),
[batch 1](benchmarks/fip-mainf939-2026-10-07/kv-batch1/results.jsonl) and
[batch 64](benchmarks/fip-mainf939-2026-10-07/kv-batch64/results.jsonl).
Protocol, finite key universe and snapshot rules remain in
[FIP-KV-CONFIG](FIP-KV-CONFIG.md).

## Limits of the evidence

Samples exclude report formatting and sorting. Allocation marks exclude the
declared initialization and warm-up. Process CPU/RSS/fault totals include
startup, raw-sample export and sorting, which can dominate a short workload.
Fresh bytes measure allocator high-water growth, not total requested bytes or
live heap. Major page faults reached 25 in one run; the retained data includes
these outliers. Page faults are not hardware cache misses, and no hardware
locality claim follows from these measurements.

Five repetitions provide observed variation, not a statistical guarantee.
The p99.9 of a 1,000-sample run lies near its maximum. Timer quantization,
scheduler delays, copying and worst-case work remain even when allocations
are zero. These in-memory experiments make no production network or storage
latency claim. Use the algorithm, ownership case and capacity policy that fit
the application, then measure its actual workload.
