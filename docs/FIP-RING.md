# Bounded ring used by two application experiments

The event loop and message broker both need bounded FIFO queues. This experiment
extracts their shared operation into an example-local `Ring[T]`, then uses it in
explicit successor modules. It adds no standard-library collection family.
The original measured applications remain unchanged.

Integration with merged main `735c646ea7eb4a1443dddb4ae78cd2d2a070bef5`
passed the complete required-target ring suite and repository lint. Fresh native
pilots produced all five comparison executables byte-identically to the measured
main11a versions. The measurements below retain their original compiler labels.

The scalar queue benchmark found little throughput difference between the
generic ring and a scalar inline ring. The broker became slower with the generic
ring in every measured group median. Both findings matter: removing duplicated
queue bookkeeping did not make the application faster.

## Capacity and ownership

`new_ring(capacity, empty)` accepts capacities 1 through 4096. It validates before
allocating storage and returns `None` for invalid capacity. Construction is an
ordinary allocating operation. `push` and `drop` consume their ring under strict
FIP contracts; readers borrow it. Status 0 means success, 1 full, and 2 empty.
Full and empty refusals preserve the logical sequence. The queue never grows.

The caller supplies an immutable sentinel. `drop` clears the removed slot to
that value, releasing the queued reference. The sentinel itself remains retained
for the lifetime of the ring. `at_or` and `front_or` return a caller-supplied
fallback for missing elements, avoiding an allocating result container.
Payload construction belongs to the caller and may allocate. Zero allocations
for a unique queue do not imply zero allocations for shared snapshots or payloads.

`broker_ring.fern` stores pool indices in one ring per subscriber. Its original
atomic fanout, producer and subscriber leases, message lifetime, cancellation
and overload behavior remain intact. `event_loop_ring.fern` uses an event ring
and a response ring around the same fixed table and linear lookup rules.
Both applications remove the nested queue reference from their owned state before
consuming the queue, then replace it. They use a borrowed accessor and a prebuilt
placeholder. Direct extraction was rejected with E068 by the current compiler.
Explicit `Ring[T]` record literals were also necessary; bare generic literals
were refused during typed lowering. Neither finding required a compiler change.

The event-loop successor accepts preconstructed immutable event records. Its
drain drops every response, so it takes linear work in the response count.
The original packed event loop resets its output count in constant work. There
is no event-loop throughput claim here; its integration is validated for behavior,
allocation and snapshots. Storage completion slots were not replaced with FIFO
queues because completion order can differ from submission order.

## Correctness

Required-target tests run on x86 Linux, ARM Linux and WASI with strict IR,
sanitizer and balanced allocation census. A separate slice FIFO checks every
logical queue element through wraparound, full/empty refusal and snapshots.
Record tests check cleared references and the maximum capacity. Negative
contracts reject allocating constructors and fresh payloads in FIP/FBIP code.

The broker successor runs the existing independent object/slice protocol oracle,
including asymmetric fanout, leases, snapshots and capacity limits. Only logical
queue access in the subject assertions changes. The event loop uses an independent
dictionary/slice model and checks every table entry, queued event, response and
counter. Pinned tests fill 4096 events, exercise input/output overflow, preserve
an entire state snapshot and reject invalid constructors before allocation.
Unique state makes zero allocations from its first data-plane operation.

The benchmark driver tests independently verify FIFO results, checksums, ordered
samples and all reported percentiles. Native harnesses check every run against
their own arithmetic oracle, retaining raw samples and exact source snapshots.
Invalid driver arguments are rejected rather than truncated or wrapped.

## Measurement setup

The native primary compiler is built from merged main
`11a71739408e54e69ce5a3db5bf8e2136ad8316c`, using official pin
`stage0-20261006-0d42e82`. Bootstrap stages 2/3 were byte-identical.
Compiler SHA-256:
`b5f886d2ee1c8e1165606426913c23648fb24b09697d62de4395cc1189ddd894`.
The [queue metadata](benchmarks/ring-2026-10-07/queue/metadata.json) and
[broker metadata](benchmarks/ring-2026-10-07/broker/metadata.json) record host,
binary/source hashes, compile commands and measurement boundaries.

Five-turn native pilots preceded 1000-turn runs and wider capacity ranges.
Five repetitions rotate configuration order. The scalar comparison has 300 runs:
five capacities, three representations, flow/full modes and unique/shared state.
The broker comparison has 400 runs: two subscriber counts, five occupancies,
two representations, both modes and both payload-sharing controls.

Sample arrays are preallocated. Startup includes queue and sample storage plus
initial fill; fill allocation counts are also reported separately. Scalar i64
payloads require no payload-container allocation. The broker's startup includes
its message pool and initial payloads. Operation samples exclude startup,
export and sorting. Wall time includes alias checks and sample storage.
Process CPU/RSS/page faults include the whole process, including export and
sorting. Fresh bytes measure high-water growth, not total requested bytes.
Page faults do not measure hardware cache misses.

## Scalar queue results

Flow removes one item and appends the next serial. The generic and inline rings
use fixed backing storage and the same indexing and clearing algorithm.
The ordinary array uses the existing `std/array.drop` suffix copy followed by
append. It is a concrete existing alternative, not a claim about the best possible
array queue implementation. All final logical elements are checked after timing.

The table shows median wall nanoseconds per turnover, followed by observed
minimum/maximum across five runs, for unique state.

| Capacity | Generic ring | Inline ring | Ordinary array |
|---:|---:|---:|---:|
| 1 | 33.875 (32.459..34.333) | 33.625 (32.500..34.125) | 44.166 (43.958..61.000) |
| 8 | 34.083 (33.750..34.291) | 33.709 (33.333..33.834) | 53.625 (53.166..54.166) |
| 64 | 34.125 (33.458..34.250) | 33.708 (31.750..33.833) | 125.584 (117.666..150.500) |
| 512 | 34.000 (32.250..34.167) | 33.667 (31.875..35.750) | 582.459 (580.291..619.042) |
| 4096 | 33.666 (33.583..34.334) | 33.750 (33.500..33.958) | 4137.500 (4018.375..4182.125) |

Generic and inline ranges mostly overlap. Their operation p99 medians are 42ns
at every listed capacity, so timer granularity and instrumentation limit what can
be concluded about tiny differences. The ordinary array's p99 median reaches
5084ns at capacity4096; its suffix copying grows with capacity.

Both fixed rings make zero unique flow allocations. The array makes respectively
1000, 2000, 5000, 8000 and 11000 allocations per 1000 turns at these capacities.
All unique full-refusal runs make zero allocations. Retaining the whole old queue
across each operation makes the fixed rings allocate twice per flow turn and
once per full refusal. The array adds one allocation per shared turn relative
to its unique counterpart. The driver reads the old endpoints after mutation;
the unique control performs equal reads before mutation.

[All queue runs](benchmarks/ring-2026-10-07/queue/results.jsonl) retain every
p50/p95/p99/p99.9/max, startup, allocation, fresh-byte, CPU/RSS and fault result.

## Broker results

The same driver and independent oracle run against both modules. The harness
adapts only the core module and logical queue reads. A flow turn delivers and
acknowledges one message for every subscriber, then reserves and publishes its
replacement. A full turn refuses a producer publish. The shared control retains
an external `Message` across the operation, not the entire broker.

For four subscribers with unique messages:

| Occupancy | Inline wall ns/turn | Generic ring wall ns/turn | Inline/ring operation p99 medians |
|---:|---:|---:|---:|
| 1 | 249.833 (233.875..256.458) | 329.917 (317.292..341.208) | 250 / 334 |
| 64 | 250.917 (241.667..256.917) | 332.584 (329.667..339.500) | 250 / 334 |
| 4095 | 249.666 (247.250..252.250) | 333.125 (323.458..351.791) | 250 / 334 |

Across all subscriber/occupancy/sharing groups, the generic-ring wall median is
1.144..1.353 times the inline median for flow and 1.024..1.150 times for refusal.
This is an application-level cost, unlike the small scalar-kernel difference.
The prototype changes a flattened queue array into nested queue records and
adds donor-detachment work; this experiment does not attribute the cost to a
single mechanism or claim a cache explanation.

Both layouts make zero allocations in unique flow and all refusal runs. Shared
flow makes one allocation per turn and 56 bytes of fresh high-water growth over
1000 turns. Recycling does not make those 1000 allocations disappear.
[All broker runs](benchmarks/ring-2026-10-07/broker/results.jsonl) preserve the
full ranges and process metrics. The generic ring remains an explicit example
successor, not a replacement for the faster measured broker control.

## Reproduce

Run `scripts/bench-ring-queue.py` or `scripts/bench-ring-broker.py` with
`uv run --no-project python`, `--compiler` pointing to a native primary compiler,
and a fresh `--output` directory. Defaults provide a small five-turn pilot.
Increase `--turns`, then capacities or occupancies, then `--repeats` separately.
The harnesses save raw ordered samples, generated drivers and source snapshots
with each run. The recorded repeated-run directories are
`/tmp/fern-ring-queue-repeated` and `/tmp/fern-ring-broker-repeated` on the
measurement host. Their compact metadata, oracle answers and per-run results
are stored alongside this report.
