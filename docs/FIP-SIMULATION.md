# Bounded simulation with immutable worlds

This experiment for #9587 runs the same deterministic simulation using a
persistent vector of entities, an FBIP entity array and FIP scalar arrays.
Both bounded representations avoid allocation with uniquely owned state and
outperform this persistent-vector implementation on the measured workload.
FIP does not consistently outperform FBIP. Retaining a world snapshot forces
copying in both, while preserving its values.

## Rules, bounds and representations

Entities inhabit a 4096 by 4096 torus. Each has position, velocity, health,
kind, an optional target and an idle/chase/recover state. A tick applies input
and AI, moves entities, rebuilds a spatial grid, counts nearby entities,
updates health and state, and computes a checksum. Integer arithmetic makes
the rules deterministic across targets. The initial state is either spread
through the world or clustered in an eight-by-eight region.

Proximity uses a radius of 128 and a 16 by 16 grid. All representations use
the same rules, grid and phase boundaries:

| Variant | World storage and replacement |
|---|---|
| `persistent` | A `core/pvec` vector of entity records, using ordinary `get` and `with`. |
| `fbip` | An owned array of entity records; remove the collection's reference before consuming a record donor. |
| `fip` | Owned scalar arrays for entity fields, with bounded updates and no record construction in the tick. |

The persistent case is an ordinary PVec implementation, not a claim that no
better persistent algorithm exists. Its element stays in the old collection
until replacement, so the record cannot be reused in the same way as the FBIP
donor. The experiment includes that cost rather than concealing it behind a
different collision algorithm.

World construction refuses entity counts outside 0..4096 before allocation.
Each world has a fixed entity count; stepping does not grow it. The grid has
256 heads and at most one linked entry per entity. A tick examines at most
N squared candidates, including each entity itself; self is excluded from
the neighbor count. Sparse placement reduces candidate work, but a dense
cluster still costs N squared. An allocation contract does not bound a tick
to constant time.

A world refuses another step after one million completed ticks, retaining its
state and recording refusal. The driver reserves one tick for warm-up and
accepts 1..999999 measured ticks. Invalid arguments are refused explicitly.
There is no queue or hidden growth policy in this model.

## Measurement

The native runs used an Apple M3 Pro on macOS 15.8, arm64, with primary compiler
revision `882e71060e9cde33b14a44103b1c99121091751c` and official pin
`stage0-20261006-0d42e82`. Native bootstrap stages 2 and 3 were byte-identical,
SHA-256 `9f95e090d983014c82cd8c2b864d9d50fb570da9d5cf0bc377506f86bca05ff7`.
The simulation executable hash was
`72b8bb78900dbb49eefdc2c3cd3b23368016bc40a030fb6a613c71478c9764d1`.
The compiler at merged revision `97a6004e5a228d0dc7cafe2f6b669fea9b6e0c00`
produces the same executable byte-for-byte. Earlier results from `f009e7a36`
remain in the [historical records](benchmarks/simulation-2026-10-07/f009/).

A five-tick pilot at 33 entities validated the complete pipeline first.
Entity-count pilots then covered 129, 513, 2048 and 4096. A separate 1000-tick
pilot preceded five repetitions of 1000 measured ticks at each of 33, 129 and
513 entities, for both layouts and both sharing modes: 180 runs in total.
Configuration order rotates between repetitions. Raw ordered samples,
source and compiler hashes, commands and per-run results are retained.

One warm-up tick precedes the allocation and timing marks. The sample buffer
is allocated before those marks, and sample export and sorting follow them.
Every timed iteration includes an extra checksum traversal. In unique mode,
it reads the current world before stepping; in shared mode, it reads the saved
world after stepping. That keeps the snapshot alive across the update and
checks that it was not mutated. Both modes perform the extra traversal.
Wall time also includes instrumentation and invariant checks.

The harness checks answers against an independent Python coordinate-sweep
oracle, rather than reusing the subject's linked grid. It verifies every
reported percentile against the ordered samples and requires all three
representations to report identical candidate work.

## Throughput and latency

The following results use uniquely owned worlds. Throughput is ticks/second;
each value is the median of five runs and each range is their observed minimum
and maximum, not a confidence interval.

| Entities | Layout | Variant | Ticks/s median | Observed range |
|---:|---|---|---:|---:|
| 33 | spread | persistent | 102878.5 | 99625.6-103307.1 |
| 33 | spread | fbip | 157964.4 | 148170.1-158167.4 |
| 33 | spread | fip | 163513.3 | 162310.2-164347.5 |
| 33 | cluster | persistent | 53060.7 | 49303.5-53816.6 |
| 33 | cluster | fbip | 111209.5 | 110579.7-111565.6 |
| 33 | cluster | fip | 114656.4 | 112697.2-115029.0 |
| 129 | spread | persistent | 18277.3 | 17463.6-18471.1 |
| 129 | spread | fbip | 36736.0 | 36202.9-38104.6 |
| 129 | spread | fip | 39498.3 | 38571.4-39599.2 |
| 129 | cluster | persistent | 4701.1 | 4526.8-4732.4 |
| 129 | cluster | fbip | 15420.1 | 14950.4-15619.8 |
| 129 | cluster | fip | 15216.4 | 15106.3-15690.4 |
| 513 | spread | persistent | 3332.2 | 3130.3-3355.6 |
| 513 | spread | fbip | 7187.7 | 7084.7-7505.2 |
| 513 | spread | fip | 7458.2 | 6901.4-7530.7 |
| 513 | cluster | persistent | 339.5 | 335.3-342.2 |
| 513 | cluster | fbip | 1357.2 | 1305.1-1377.1 |
| 513 | cluster | fip | 1323.2 | 1312.8-1345.5 |

Tick latency below is in nanoseconds, as medians of the five per-run percentiles.
Each run has only 1000 samples, so p99.9 is its second-largest sample. It cannot
establish a rare-event bound or a production service-level guarantee.

| Entities | Layout | Variant | p50 | p95 | p99 | p99.9 |
|---:|---|---|---:|---:|---:|---:|
| 33 | spread | persistent | 9666 | 10125 | 10584 | 12583 |
| 33 | spread | fbip | 6291 | 6625 | 7083 | 8125 |
| 33 | spread | fip | 6042 | 6417 | 6959 | 7875 |
| 33 | cluster | persistent | 18667 | 20041 | 21334 | 23917 |
| 33 | cluster | fbip | 8916 | 9666 | 10500 | 11583 |
| 33 | cluster | fip | 8584 | 9542 | 10333 | 11375 |
| 129 | spread | persistent | 54042 | 60000 | 64375 | 67792 |
| 129 | spread | fbip | 26959 | 28833 | 31125 | 34209 |
| 129 | spread | fip | 25125 | 26791 | 29583 | 32833 |
| 129 | cluster | persistent | 210542 | 223666 | 230042 | 254166 |
| 129 | cluster | fbip | 64708 | 67583 | 73334 | 77375 |
| 129 | cluster | fip | 65209 | 69333 | 74500 | 79167 |
| 513 | spread | persistent | 299625 | 310042 | 317292 | 345500 |
| 513 | spread | fbip | 138625 | 149708 | 156916 | 160709 |
| 513 | spread | fip | 132917 | 146541 | 161667 | 169875 |
| 513 | cluster | persistent | 2907417 | 3084917 | 3185708 | 3432500 |
| 513 | cluster | fbip | 733917 | 767125 | 802875 | 860916 |
| 513 | cluster | fip | 748709 | 785000 | 818500 | 856291 |

FIP and FBIP have overlapping throughput ranges in several groups. At 513
spread entities, FIP's p99.9 is higher than FBIP's despite its higher
median throughput. Zero allocations did not remove all timing variability.

## Sharing, allocations and memory

Allocations per tick were identical across repetitions and layouts:

| Entities | Persistent unique/shared | FBIP unique/shared | FIP unique/shared |
|---:|---:|---:|---:|
| 33 | 99 / 107 | 0 / 38 | 0 / 11 |
| 129 | 387 / 403 | 0 / 134 | 0 / 11 |
| 513 | 1539 / 1579 | 0 / 518 | 0 / 11 |

At 513 entities, the shared-mode median p99 values were:

| Layout | Persistent, ns | FBIP, ns | FIP, ns |
|---|---:|---:|---:|
| spread | 364666 | 155667 | 149375 |
| cluster | 3165542 | 806041 | 820667 |

All unique runs reported zero steady fresh bytes, including the allocating
persistent variant, whose freed blocks were recycled. At 513 entities, shared
runs grew the allocator's high-water mark by 67,416 bytes for persistent,
72,400 for FBIP and 48,800 for FIP over 1000 ticks. These are growth totals,
not requested allocation bytes or peak resident memory. The driver retains
one old world at a time; it does not model an indefinitely growing history.

Startup and whole-process resources below are medians for the 513-entity
spread case with unique worlds. Startup includes construction, one warm-up
tick and the sample buffer.

| Variant | Startup, ns | Startup allocations | Startup fresh bytes | User CPU, s | System CPU, s | Peak RSS, bytes |
|---|---:|---:|---:|---:|---:|---:|
| persistent | 274333 | 2175 | 76256 | 0.301228 | 0.003537 | 1245184 |
| fbip | 129042 | 527 | 82752 | 0.140174 | 0.003834 | 1245184 |
| fip | 127375 | 13 | 62112 | 0.134677 | 0.003454 | 1228800 |

CPU and RSS cover the whole process, including startup, sample export and
sorting. Raw repeated results retain major-page-fault outliers of 16 and 23.
No hardware cache counters were collected. The scalar arrays and entity
collections have different access patterns, but these timings cannot isolate
cache effects from record access, reference counting or arithmetic.

## Maximum capacity and practical limits

The five-tick pilot at the maximum 4096 entities passed every representation,
layout and sharing mode. Its dense case examined 83,886,080 candidates, exactly
5 times 4096 squared; the spread case examined 2,949,276. Unique FIP and FBIP
still allocated nothing. With a retained snapshot, FIP allocated 55 times
over five ticks and FBIP 20,505 times. Those counts describe different-sized
copies and must not be treated as a comparison of bytes copied.

Five ticks are capacity and correctness evidence, not enough samples for a
meaningful tail distribution. Larger dense worlds need a different proximity
algorithm if quadratic work is unacceptable. Adding `fip` cannot solve that.
The state-specific arrays stay local to this experiment; no shared collection
API was needed.

## Reproduction and validation

```sh
make bootstrap distcheck
uv run --no-project python scripts/bench-simulation.py --compiler build/bootstrap/stage3 --entities 33 --ticks 5 --repeats 1 --output /tmp/simulation-pilot
uv run --no-project python scripts/bench-simulation.py --compiler build/bootstrap/stage3 --entities 33 --ticks 1000 --repeats 1 --output /tmp/simulation-tick-pilot
uv run --no-project python scripts/bench-simulation.py --compiler build/bootstrap/stage3 --entities 33 --ticks 1000 --repeats 5 --output /tmp/simulation-33
```

Then change only the entity count to 129 and 513, using a new output directory
for each run. For maximum-capacity checks, keep the five-tick pilot and raise
only its entity count through 129, 513, 2048 and 4096. Timing requires a native
host. The harness preserves commands and refuses reuse of an output directory.

Compact records for [33](benchmarks/simulation-2026-10-07/n33/metadata.json),
[129](benchmarks/simulation-2026-10-07/n129/metadata.json) and
[513](benchmarks/simulation-2026-10-07/n513/metadata.json) entities include
metadata, independent oracle answers and all per-run results. The
[4096-entity pilot](benchmarks/simulation-2026-10-07/n4096/metadata.json) is
retained separately. Full samples and source snapshots remain under
`/tmp/fern-simulation-main882-n{33,129,513}-repeated/` and
`/tmp/fern-simulation-main882-n4096/`. Earlier samples remain under the
corresponding `mainf009` paths.

The Go tests use a separate all-pairs oracle to compare every entity field,
checksum and snapshot across required x86-64 Linux, ARM64 Linux and WASI
executions with strict IR, sanitization and balanced allocation censuses.
Boundaries cover radius 128 versus 129, torus wrapping, half-torus ties,
health transitions, empty and maximum worlds, invalid capacity, tick refusal
and E068 rejection of fresh entity construction in allocation-free contracts.
Driver tests check sample counts, percentiles and invalid arguments. Full
local target validation and lint passed on the recorded compiler revision.
