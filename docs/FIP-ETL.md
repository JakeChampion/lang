# Bounded telemetry ETL

Latest compiler measurements: [merged main11a validation](FIP-MAIN11A.md).
The measurements below retain their original compiler and date.

This experiment for #9591 compares four Fern implementations of the same
decode, validate, normalize, filter, aggregate and encode pipeline. On the
measured Apple M3 Pro, the ordinary per-record baseline was fastest. The measured
compiler eliminated its record allocations; FIP and FBIP also allocated nothing
in steady state. Their different throughput shows that allocation counts alone
do not predict speed.

## Workload and ownership

[`etl_core.fern`](../examples/fip/etl_core.fern) generates deterministic
eight-byte records: sensor and quality bytes, a signed little-endian 16-bit
temperature, and an unsigned four-byte sequence. Generation happens before
measurement. The fixture deliberately mixes invalid sensors, invalid qualities,
out-of-range temperatures and filtered qualities.

Valid temperatures range from -4000 through 12500 inclusive. Qualities 0 and 1
are filtered; 2 and 3 proceed. Normalization adds 4000. Every accepted record
updates a count, sum, minimum, maximum and 16-bin histogram, then emits the
normalized value and original sequence in eight bytes. This is fixed-width
telemetry, not a claim about arbitrary JSON, CSV or string-heavy ETL.

All variants share validation, aggregation and encoding:

| Variant | Decoding and intermediate storage |
|---|---|
| `baseline` | Construct one record and process it immediately; the compiler eliminates the temporary box. |
| `batched` | Decode a batch into an array of records, then aggregate it. |
| `fbip` | Rebuild an owned scratch record and reuse it for each input. |
| `fip` | Decode into scalars and update the owned bounded state directly. |

The state owns a histogram and a batch-sized output region. Callers consume
the valid output prefix before the next batch. The driver reads every emitted
byte into a checksum. The same byte order and aggregation rules apply to every
variant and every batch size.

Capacity is explicit. A count exceeding capacity, a negative count or offset,
or a request extending beyond the available bytes is refused before processing
any records. Refusal increments `refused`, clears `output_count`, and preserves
aggregates. An empty batch is valid, including with zero capacity. Invalid
records within a valid batch increment `invalid`; they do not refuse the batch.

`own` permits consumption of the passed value; it does not prove runtime
uniqueness. Retaining a prior state or its output invokes copy-on-write when
needed. The zero-allocation results below concern warmed, uniquely owned state.
Tests retain the complete output and histogram across a subsequent update and
check that every old byte and bin remains unchanged.

## Measurements

Five runs per variant and batch processed 2,000,000 records each on macOS 15.8,
arm64, Apple M3 Pro. Run order rotates between repetitions. A 4,096-record
pilot verified the same measurement pipeline before scaling. These are native
measurements; Linux x86-64, ARM64 and WASI executions supply separate correctness
evidence.

These measurements were refreshed on 2026-10-07 using the primary Fern compiler
built from `f009e7a36be99cb883f35430e8ee29aa5eff5802` with the official
`stage0-20261006-0d42e82` pin. Bootstrap stages 2 and 3 were byte-identical.
The measured executable SHA-256 is
`7ca867ad64b982b1ae8e37970d2931a2c35a689e15ddb71b113a39d03c589688`.
The [compiler record](benchmarks/etl-2026-10-07/compiler.json) preserves its
hash, source revision and build command.

Throughput includes batch processing, reading every output byte and recording
latency samples. Batch latency ends before output consumption. Allocation
marks exclude input generation, warm-up, reset, sample-buffer construction and
reporting. Raw samples are exported after the measured region; sorting for
percentiles also happens afterward.

Throughput is millions of records/second. Ranges are the minimum and maximum
of five runs, not confidence intervals. The p99 column is the median of the
five per-run batch p99 values.

| Batch | Variant | Throughput median | Observed range | Batch p99, ns | Allocations/record |
|---:|---|---:|---:|---:|---:|
| 1 | baseline | 25.527 | 24.863-26.064 | 83 | 0 |
| 1 | batched | 17.575 | 16.886-17.768 | 84 | 2 |
| 1 | fbip | 16.588 | 15.471-16.879 | 84 | 0 |
| 1 | fip | 23.136 | 22.384-23.221 | 84 | 0 |
| 64 | baseline | 56.683 | 54.844-57.725 | 1291 | 0 |
| 64 | batched | 42.102 | 41.957-42.224 | 1750 | 1.078125 |
| 64 | fbip | 37.816 | 36.853-38.075 | 2000 | 0 |
| 64 | fip | 52.484 | 50.730-53.033 | 1417 | 0 |
| 1024 | baseline | 60.737 | 58.673-61.207 | 18916 | 0 |
| 1024 | batched | 43.474 | 43.098-44.720 | 26375 | 1.0087915 |
| 1024 | fbip | 37.499 | 37.354-39.303 | 30292 | 0 |
| 1024 | fip | 53.876 | 52.758-55.856 | 20875 | 0 |

Every run reported zero steady-state **fresh bytes per record**, including the
allocating batched variant. The allocator recycled its blocks. This measures growth
of the allocator's high-water mark, not total requested allocation bytes and
not resident memory. Replacing it with allocation count would lose that distinction.

At batch 1024 the median per-run latency percentiles were:

| Variant | p50, ns | p95, ns | p99, ns | p99.9, ns |
|---|---:|---:|---:|---:|
| baseline | 14958 | 15959 | 18916 | 19667 |
| batched | 21709 | 23875 | 26375 | 27542 |
| fbip | 25250 | 26416 | 30292 | 37500 |
| fip | 17041 | 18750 | 20875 | 21834 |

Batch 1's median p99 values are 83-84 ns. Timer resolution and per-batch
instrumentation limit their usefulness at this scale; the one-nanosecond
difference does not establish a meaningful tail-latency advantage.

Whole-process CPU and peak RSS at batch 1024, again medians of five runs:

| Variant | User CPU, s | System CPU, s | Peak RSS, bytes | Startup allocations |
|---|---:|---:|---:|---:|
| baseline | 0.046302 | 0.006581 | 17235968 | 5 |
| batched | 0.059775 | 0.006727 | 17285120 | 1038 |
| fbip | 0.067090 | 0.007626 | 17235968 | 6 |
| fip | 0.051103 | 0.006338 | 17219584 | 5 |

These process figures include dataset generation, warm-up, sample export and
report sorting. At batch 1, exporting two million lines dominates system CPU:
the medians range from 4.292622 to 4.524880 seconds. Peak RSS has a median of
83,279,872 bytes for every variant there. The sample array and reporting are
part of those totals, so they do not describe the data plane's memory alone.
The median major-page-fault count was zero in every group; individual runs
also recorded 27 major faults. The per-run data retains that outlier.

At batch 1024, median startup times were 13.041125 ms for baseline, 13.263084 ms
for batched, 13.400958 ms for FBIP and 13.260833 ms for FIP. Startup fresh bytes
were 16,804,128, 16,881,496, 16,804,184 and 16,804,128 respectively. These include
the two-million-record input, warm-up and measurement storage, as well as the
bounded histogram and output region.

## Locality and limits

Input and output are contiguous byte regions in all variants. Ordinary batching
additionally retains an array of decoded record boxes until aggregation; the
baseline's temporary boxes disappear under optimization. FBIP reuses one scratch record, and
FIP has no decoded-record box in its inner loop. The measurement shows that
materializing these ordinary batches does not repay its cost for this workload.
It does not isolate cache misses from construction, reference counting or
instruction overhead. No hardware cache counters were collected, and page
fault counts must not be presented as cache-miss counts.

The compiler accepted sequential field spreads for scratch-record reuse. A
single spread replacing every field was rejected with E068 because the lowered
construction had no recognized donor. The range-loop helper was not admitted
by the FIP check; bounded `while` loops express the accepted form. These are
concrete restrictions an author encounters, not evidence that annotations
automatically optimize ordinary code. The native FBIP pilot allocated once on
its first call and zero times on the next; the cause of that cold allocation
has not been established.

For this bounded format, the ordinary implementation is the fastest measured
option. FIP supplies a checked allocation contract and explicit scalar decoding;
FBIP supplies a checked reuse contract but costs more here. Neither annotation
is a reason to replace faster ordinary code without an application requirement
for that contract. No shared collection abstraction is needed for this state.

The [earlier results](benchmarks/etl-2026-10-06/results.jsonl) used the same source
with compiler revision `9d3616f76532851d4b37f2ef5fba3467e7989537`. That build
allocated one record per baseline input and ranked FIP first. The current
compiler eliminates those allocations and reverses the ranking. No opaque
call boundary was added to force the baseline to keep allocating. This is why
the report records the compiler revision as well as the source and workload.

## Reproduction and validation

### Merged compiler checkpoint

After this experiment merged, the compiler at
`882e71060e9cde33b14a44103b1c99121091751c` produced a different executable.
The same 4096-record pilot and all 60 two-million-record runs were repeated;
every output and sample check passed. The original tables above remain labeled
with their `f009e7a36` compiler. The new medians and ranges are:

| Batch | Variant | Million records/s median | Observed range | Batch p99, ns |
|---:|---|---:|---:|---:|
| 1 | baseline | 25.130 | 24.861-25.951 | 83 |
| 1 | batched | 17.141 | 16.184-17.788 | 84 |
| 1 | fbip | 16.339 | 16.267-16.782 | 84 |
| 1 | fip | 22.453 | 21.867-22.688 | 84 |
| 64 | baseline | 56.506 | 54.387-58.056 | 1167 |
| 64 | batched | 40.901 | 40.333-41.739 | 1708 |
| 64 | fbip | 36.570 | 36.168-38.103 | 1917 |
| 64 | fip | 51.255 | 51.046-53.528 | 1292 |
| 1024 | baseline | 59.467 | 58.325-61.683 | 18792 |
| 1024 | batched | 44.664 | 43.207-45.767 | 23583 |
| 1024 | fbip | 38.519 | 37.775-39.529 | 28958 |
| 1024 | fip | 55.984 | 54.287-56.217 | 18875 |

Allocation counts and fresh-byte growth were unchanged. The ordinary baseline
still leads throughput, and FBIP remains slower despite zero steady allocations.
All per-run percentiles, startup and process resources are in the
[merged-checkpoint results](benchmarks/etl-2026-10-07-main882/results.jsonl),
with [metadata](benchmarks/etl-2026-10-07-main882/metadata.json),
[oracle](benchmarks/etl-2026-10-07-main882/oracle.json) and
[compiler record](benchmarks/etl-2026-10-07-main882/compiler.json).
Full samples remain in `/tmp/fern-etl-main882-repeated/`.
Compiler revision `97a6004e5a228d0dc7cafe2f6b669fea9b6e0c00` was subsequently
verified to produce this same executable byte-for-byte, so those measurements
also describe that build. Both compilers reached native stage 2/3 byte identity.

### Commands and coverage

Build the primary compiler and the example, then run the pilot before scaling:

```sh
make bootstrap distcheck
build/bootstrap/stage3 -target arm64-darwin -o /tmp/etl examples/fip/etl.fern internal/stdlib
uv run --no-project python scripts/bench-etl.py --binary /tmp/etl --records 4096 --batches 1 64 1024 --repeats 1 --output /tmp/etl-pilot
uv run --no-project python scripts/bench-etl.py --binary /tmp/etl --records 2000000 --batches 1 64 1024 --repeats 5 --output /tmp/etl-full
```

Use the host's native target for timing. Each output directory must be new.
The harness stores ordered samples, source snapshots, hashes, independent
expected aggregates and per-run results. It checks every checksum and recomputes
all reported percentiles from the exported samples.

The [recorded results](benchmarks/etl-2026-10-07/results.jsonl),
[metadata](benchmarks/etl-2026-10-07/metadata.json) and
[oracle](benchmarks/etl-2026-10-07/oracle.json) preserve this run's comparisons.
The full ordered samples are retained locally in
`/tmp/fern-etl-mainf009-repeated/`; rerunning the commands produces fresh samples.

`TestSelfHostFipETL` compares every generated and encoded byte with an independent
Go reference for batches 1, 7, 64 and 257. It also checks aggregates, immutable
snapshots, partial and refused batches, zero steady-state allocations and
balanced allocation censuses. Boundary tests pin signed values, validation
endpoints, histogram transitions, unsigned sequences and complete saved outputs.
Negative tests require E068 for allocating FIP/FBIP decoders. Driver tests run
all variants and sample export through the primary compiler on all three
required targets. Native bootstrap stages 2 and 3 were byte-identical on
merged main, and full lint passed.
