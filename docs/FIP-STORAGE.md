# Bounded storage and page-cache simulation

The storage experiment for #9590 uses preallocated request and completion slots,
cache frames and an in-memory fake disk. Its unique data plane makes no allocations
from the first operation in the tested workloads. Retaining a whole-state snapshot
requires copies and preserves the old values. This is a deterministic simulator;
the measurements describe native processing cost, not physical I/O.

## Protocol and bounds

[storage_core.fern](../examples/fip/storage_core.fern) allocates configuration.
[storage_fip.fern](../examples/fip/storage_fip.fern) supplies strict FIP
`submit`, `advance` and `collect` transitions. A request moves from queued to
scheduled to completed; collection releases its slot. Completed but uncollected
requests still consume capacity, so a slow consumer cannot silently lose results.

Configuration accepts 1..256 disk pages, 1..128 cache frames (at most the page
count), 1..256 words per page, 1..128 outstanding requests and a visit batch from
1 through capacity. Invalid configurations are refused before allocation.
Each advance increments the logical clock and visits exactly the configured
number of slots. A visited slot makes at most one transition, so even a zero-delay
request completes on a later visit. Delays are logical ticks, without host sleeps.

Reads and writes load missing pages into a clean, unpinned frame. Replacement
uses the least recently touched frame, with frame index breaking ties; unused
frames come first. Scheduled operations pin both their page and frame.
Conflicts, a full cache, an uncached flush/evict and dirty eviction produce
explicit completion errors. Successful writes change cache contents; only a
successful flush changes the fake disk. Injected completion faults release
reservations while preserving data, dirty bits and replacement metadata.

Submission refuses bad inputs, a full queue and exhausted serial/clock values.
Collection validates both slot and ticket, distinguishing pending and stale
requests. Tickets never wrap. At the terminal i64 clock, advance explicitly
refuses without draining pending work; callers must avoid exhausting that bound.

## Correctness

The independent Go object/slice model compares the complete state after each
operation on x86 Linux, ARM Linux and WASI. Boundary tests cover LRU ties,
successful and faulted reads/writes/flushes/evictions, retry durability,
asymmetric delays, completion backlog, stale tickets, pin conflicts, maximum
capacities, signed payload limits, deadline overflow and terminal counters.
Retained whole-state snapshots and allocating FIP/FBIP negative controls are
checked separately. Target runs enable strict IR, sanitization and balanced
allocation census checks.

The benchmark driver is checked against that Go model on all three targets.
Its Python harness uses a separate object/event model and verifies every
deterministic reported field, ordered sample and percentile. Malformed arguments
are refused. These checks have passed locally; merged acceptance also requires
the workstream's full CI gate.

## Workload and measurement

[storage.fern](../examples/fip/storage.fern) accepts depth, wave count,
plain/delayed/faulted mode and unique/shared ownership. Each wave fills the
request queue with one operation per page, attempts one additional submission
and verifies refusal, then completes and collects the accepted requests.
Waves cycle through write, flush, read and evict, with deterministic offsets and
signed values. The delayed mode uses 17 logical ticks. Faulted mode injects an
error at every eleventh operation ordinal; some operations fail earlier because
the preceding fault left a page uncached or dirty.

Depths 1, 3, 8, 32 and 128 use the same workload: disk pages, cache frames and
request capacity equal depth, page width is eight, and the visit batch is one.
There is no warm-up exemption. Shared mode retains a whole State across a wave
and reads its data checksum afterward; unique mode performs that same traversal
before the wave. Samples cover submission, refusal, advances and collection.
Wall throughput also includes checksum traversal and sample storage.

Five-wave native pilots preceded 1,000-wave runs with the same parameters.
Five repetitions give 150 measured runs, with rotating mode/ownership order.
The compiler is built from merged main
`11a71739408e54e69ce5a3db5bf8e2136ad8316c`, official pin
`stage0-20261006-0d42e82`, on macOS 15.8 arm64. Bootstrap stages 2/3 were
byte-identical. Compiler SHA-256:
`b5f886d2ee1c8e1165606426913c23648fb24b09697d62de4395cc1189ddd894`.
The [metadata](benchmarks/fip-main11a-2026-10-07/storage/metadata.json)
records the uncommitted experiment source hashes and exact compile command;
the measured executable is
`385a493774b5471df2eb5c4d7062d5008b56c8616703f2124a30e9350d5df664`.

Merged-main integration at `4ae53835f86efcb848bb64fad167bf67cfb76331`
produces the same executable byte for byte. Its native bootstrap, fresh pilot,
three-target storage suite and full lint pass. The recorded timing runs keep
their main11a label rather than being presented as new measurements.

Throughput below is millions of completed operations per second, including error
completions. Ranges are observed minima/maxima, not confidence intervals. Tail
columns are medians of the five per-run wave percentiles; a wave contains
`depth` requests. Logical latency measures submit-to-completion simulator ticks,
independent of host execution time.

| Depth | Mode | Unique Mops/s (range) | Shared Mops/s (range) | Unique p99/p99.9 ns | Shared p99/p99.9 ns | Mean logical ticks |
|---:|---|---:|---:|---:|---:|---:|
| 1 | plain | 5.343 (2.702..5.456) | 2.831 (1.822..2.956) | 250/666 | 375/916 | 2.000 |
| 1 | delayed | 2.519 (1.587..2.570) | 1.766 (1.235..1.832) | 459/875 | 584/1250 | 18.000 |
| 1 | faulted | 5.443 (4.035..5.535) | 3.005 (2.328..3.046) | 250/958 | 375/1167 | 1.955 |
| 3 | plain | 5.603 (4.821..5.778) | 4.221 (3.780..4.300) | 666/1041 | 750/1250 | 5.000 |
| 3 | delayed | 4.163 (3.708..4.195) | 3.306 (3.100..3.367) | 792/1292 | 959/1500 | 20.000 |
| 3 | faulted | 5.409 (5.286..5.566) | 4.076 (3.773..4.197) | 625/958 | 792/1292 | 4.865 |
| 8 | plain | 6.079 (5.163..6.269) | 5.317 (5.267..5.348) | 1584/1958 | 1750/2334 | 12.500 |
| 8 | delayed | 5.375 (2.639..5.531) | 4.722 (4.665..4.946) | 1792/2417 | 1958/2417 | 28.500 |
| 8 | faulted | 6.074 (5.975..6.493) | 5.259 (4.837..5.500) | 1500/1750 | 1750/3792 | 12.135 |
| 32 | plain | 5.526 (5.223..5.987) | 5.474 (5.273..5.714) | 7375/14458 | 7250/7667 | 48.500 |
| 32 | delayed | 5.603 (5.429..5.839) | 5.300 (5.270..5.483) | 7375/8709 | 7417/9167 | 48.500 |
| 32 | faulted | 5.514 (5.066..5.712) | 5.303 (1.790..5.425) | 7209/8542 | 7250/20250 | 47.045 |
| 128 | plain | 3.686 (3.048..3.695) | 3.778 (3.486..3.976) | 50833/55625 | 50917/55084 | 192.500 |
| 128 | delayed | 3.749 (3.619..3.890) | 3.805 (2.550..3.926) | 51125/56584 | 50750/54292 | 192.500 |
| 128 | faulted | 4.100 (4.004..4.127) | 3.956 (3.887..3.974) | 41375/46250 | 42500/47500 | 186.682 |

Unique state made zero allocation events and zero fresh high-water growth in
every run, including the first wave. Plain/delayed shared runs made 17,500
allocations per 1,000 waves at every depth. Faulted shared runs ranged from
17,094 to 18,250. Shared fresh growth ranged from 936 bytes at depth one to
28,360 bytes at depth 128. Fresh growth is not total requested allocation bytes.

The delayed workload has the same logical latency as plain at depths 32 and
128: the scheduler does not revisit a slot before its 17-tick delay has elapsed.
Queue scans and cache lookup/replacement remain linear in configured capacity;
a full wave can require quadratic work. Fixed memory and bounded visits do not
make a whole wave constant time.

Shared state has slightly higher group-median throughput at depth 128 in plain
and delayed modes, with overlapping observed ranges. The measurements do not
establish a general performance benefit from retaining snapshots.

## Startup, resources and limitations

Startup includes the State, its 19 backing arrays and the sample buffer:
21 allocation events in each run. Startup time ranged from 541 to 14,375 ns,
with a 916 ns median across configurations. That mixed median is descriptive,
not a comparison at equal capacity.

Whole-process user CPU ranged from 0.000966 to 0.042949 seconds, system CPU from
0.003039 to 0.009414 seconds, and peak RSS from 1,179,648 to 1,277,952 bytes.
Those totals include startup, raw sample export and sorting. Minor faults ranged
from 279 to 318; all runs had zero major faults. Page faults are not hardware cache
misses, and no hardware locality counter was measured.

Fixed arrays keep operation state explicit, but the wide State requires many
named field replacements. Consuming each array field separately lets the
compiler verify its donor. A borrowed snapshot helper makes the shared control
explicit. The public retrieval operation is called `collect` because `poll`
is a builtin name. The private benchmark wave helper reuses scalar result fields
for its summary, avoiding an allocating instrumentation record per wave; the
public transition protocol is unchanged.

No physical disk, concurrency, crash recovery or general asynchronous-task
comparison is implemented. The experiment demonstrates bounded state-machine
storage and its ownership costs, not equivalence to an operating-system I/O stack.

## Evidence and reproduction

[Per-run results](benchmarks/fip-main11a-2026-10-07/storage/results.jsonl) retain
all percentiles, checksums, startup, allocation and process metrics.
[Oracle results](benchmarks/fip-main11a-2026-10-07/storage/oracle.json)
record deterministic expectations. Full raw samples and source snapshots were
saved under `/tmp/fern-storage-main11a-repeated/` on the measurement host.

Run the same pipeline first at five waves, then change only the wave count:

```sh
uv run scripts/bench-storage.py --compiler /path/to/primary-fern --depths 1 3 8 32 128 --waves 5 --output /tmp/storage-pilot
uv run scripts/bench-storage.py --compiler /path/to/primary-fern --depths 1 3 8 32 128 --waves 1000 --output /tmp/storage-1000
uv run scripts/bench-storage.py --compiler /path/to/primary-fern --depths 1 3 8 32 128 --waves 1000 --repeats 5 --output /tmp/storage-repeated
```
