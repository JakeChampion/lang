# FIP experiment measurements after merged scalar reuse

These runs refresh the ETL, simulation, DSP and broker evidence after the
compiler changes in merged main `11a71739408e54e69ce5a3db5bf8e2136ad8316c`.
All four native executables changed, so the earlier measurements remain
historical rather than being relabeled. The updated verifier in PR #11830
produces byte-identical experiment executables; it does not require another
timing run.

Integration at merged main `4ae53835f86efcb848bb64fad167bf67cfb76331`
also produces byte-identical ETL, simulation, DSP, broker and storage executables.
That compiler reached identical bootstrap stages 2/3 with SHA-256
`7d154e8b914f9501522c71aa480392edce8e3eee587cbef5db74e125c2c9a19c`.
The measurements below retain their original main11a provenance.

The primary compiler used official pin `stage0-20261006-0d42e82` and reached
byte-identical bootstrap stages 2/3. Its SHA-256 is
`b5f886d2ee1c8e1165606426913c23648fb24b09697d62de4395cc1189ddd894`.
Each evidence directory preserves exact compiler, binary and source hashes,
compile command, deterministic oracle results and every per-run aggregate.
Small native pilots preceded each larger configuration. All oracle and ordered
sample checks passed. Required-target integration tests passed separately on
x86 Linux, ARM Linux and WASI.

## ETL

Five repetitions at two million records, four variants and three batch sizes
produce 60 runs. The ordinary baseline is fastest at each batch's median,
with zero steady allocations. FIP and FBIP also make zero steady allocations;
the materialized batched variant allocates. All variants have zero fresh
high-water growth after warm-up.

| Batch | Variant | M records/s (range) | Steady allocations |
|---:|---|---:|---:|
| 1 | baseline | 38.298 (29.508..38.769) | 0 |
| 1 | batched | 24.564 (24.082..24.894) | 4000000 |
| 1 | fbip | 29.632 (29.344..30.297) | 0 |
| 1 | fip | 32.791 (32.637..33.476) | 0 |
| 64 | baseline | 71.683 (70.256..72.782) | 0 |
| 64 | batched | 49.885 (49.177..51.722) | 2156250 |
| 64 | fbip | 55.949 (41.240..56.599) | 0 |
| 64 | fip | 67.525 (63.761..68.558) | 0 |
| 1024 | baseline | 74.460 (72.449..76.711) | 0 |
| 1024 | batched | 54.818 (52.848..55.313) | 2017583 |
| 1024 | fbip | 58.920 (57.912..59.313) | 0 |
| 1024 | fip | 71.732 (66.440..72.796) | 0 |

[ETL results](benchmarks/fip-main11a-2026-10-07/etl/results.jsonl) include all
batch latency percentiles, startup, CPU/RSS and faults.
[Metadata](benchmarks/fip-main11a-2026-10-07/etl/metadata.json) pins the executable
and sources; the [compiler record](benchmarks/fip-main11a-2026-10-07/etl/compiler.json)
supplies its build provenance. Workload details remain in [FIP-ETL](FIP-ETL.md).

## Simulation

Five repetitions at 1,000 ticks cover 33, 129 and 513 entities, spread/clustered
layouts, all three representations and unique/shared ownership: 180 runs.
Capacity pilots also covered 2,048 and 4,096 entities before scaling.

The table below selects the 129-entity spread case. Ranges are observed
minimum/maximum values. The bounded unique representations allocate nothing;
shared copies preserve prior worlds.

| Representation | Ownership | Ticks/s median (range) | p99 tick ns | Allocations/tick |
|---|---|---:|---:|---:|
| persistent | unique | 18844 (18114..18937) | 60709 | 387 |
| persistent | shared | 18489 (17732..18548) | 62875 | 403 |
| fbip | unique | 38335 (36813..39661) | 30791 | 0 |
| fbip | shared | 38135 (36439..38360) | 31292 | 134 |
| fip | unique | 41149 (40760..41401) | 27833 | 0 |
| fip | shared | 40090 (39578..40171) | 29417 | 11 |

The complete [33](benchmarks/fip-main11a-2026-10-07/simulation-n33/results.jsonl),
[129](benchmarks/fip-main11a-2026-10-07/simulation-n129/results.jsonl) and
[513](benchmarks/fip-main11a-2026-10-07/simulation-n513/results.jsonl)
results retain clustered and shared cases too. Metadata beside each result
records the exact source and binary. The grid still has an N-squared dense
worst case; see [FIP-SIMULATION](FIP-SIMULATION.md).

## DSP and broker

DSP's 120 runs cover six block sizes, both implementations, both ownership
modes and five repetitions of 1,000 blocks. The ordinary direct loop remains
faster at every block/ownership group median. Both unique variants allocate
nothing from the first callback; shared runs make four allocations per block.
[DSP results](benchmarks/fip-main11a-2026-10-07/dsp/results.jsonl) and
[metadata](benchmarks/fip-main11a-2026-10-07/dsp/metadata.json) preserve the
negative comparison and full tails. Signal rules are in [FIP-DSP](FIP-DSP.md).

Broker's 300 runs cover one/four/sixteen subscribers, five queue occupancies,
flow/full refusal, unique/shared ownership and five repetitions of 1,000 turns.
Unique turnover and refusal make zero allocations. Shared turnover makes one
allocation per turn; fresh high-water growth is zero or 56 bytes.
[Broker results](benchmarks/fip-main11a-2026-10-07/broker/results.jsonl) and
[metadata](benchmarks/fip-main11a-2026-10-07/broker/metadata.json) retain every
group. Lifecycle and alias controls are in [FIP-BROKER](FIP-BROKER.md).

## Reading these measurements

Per-run tails and ranges are evidence from this host, not confidence intervals
or real-time guarantees. Process CPU/RSS/fault totals include startup, sample
export and sorting. Fresh bytes measure high-water growth, not requested bytes;
page faults are not hardware cache misses. No hardware locality claim is made.

Full raw samples and source snapshots remain in the measurement host's
`/tmp/fern-*-main11a-*` directories. The existing benchmark harnesses reproduce
each pipeline. These local results do not replace full merged CI and the
original issue acceptance audit.
