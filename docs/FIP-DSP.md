# Bounded offline DSP

The complete gain/filter/delay/mixer/limiter graph is FIP. With unique input
state, its allocation count stays unchanged from the first processing callback.
The ordinary direct loop also allocates nothing and is faster in these runs.
The contract makes allocation behavior explicit; it does not make this staged
graph the fastest implementation.

This experiment addresses #9588 in #9582. It is an offline mono sample processor,
not an audio-device scheduler or a real-time deadline guarantee.

## Signal and ownership contract

`examples/fip/dsp_core.fern` constructs a graph with block capacity 1..4096 and
delay length 1..8192. Construction allocates work/output buffers and the ring.
Input is finite `f64`, with deterministic silence and positive/negative peaks.
Each block follows these operations in order:

1. Gain multiplies the sample by 1.5.
2. A causal FIR uses 0.5 of the gained sample and 0.25 of each previous gained sample.
3. A ring delay reads its old slot and writes the filtered sample plus 0.25 of that old value.
4. The mixer combines 0.75 filtered and 0.5 delayed signal.
5. The limiter clips to [-1,1].

The FIP implementation composes five consuming functions over one owned graph.
The direct implementation uses ordinary indexed replacement in one loop. It
performs the same arithmetic, retains the same filter/ring state, and writes the
same final scratch/output values. Neither implementation has opaque subject
calls or artificial allocations. No new stdlib API was needed.

Negative counts, counts above capacity or input length, and sample-counter
overflow are refused before changing DSP state. A refusal sets `refused` and
clears `output_count`. Empty blocks leave signal state unchanged. Constructor
refusals occur before allocation. A retained graph remains an immutable snapshot;
FIP does not promise allocation-free replacement of shared storage.

## Correctness

The independent Go scalar oracle checks every output, delay slot, filter value,
cursor and sample count across several blocks. Tests include single-sample blocks,
short and long delays, 257-sample blocks, impulse/silence/clipping, ring wrapping,
maximum capacities, invalid lengths/counts, i64 overflow, zero and partial blocks,
partition invariance, retained snapshots and rejected allocating FIP/FBIP stages.
Expected constants are constructed outside allocation marks. FIP allocation
checks include the first callback, without a warm-up exemption.

Required x86-64 Linux, ARM64 Linux and WASI tests run with strict IR, sanitizer
and balanced allocation census. Driver tests check the independent stream
checksums, ordered samples, reported percentiles and malformed arguments.
On merged main `4e6f4d29c`, the driver pilot passed in 16.051 seconds and the combined DSP
suite in 70.866 seconds. Native bootstrap stages 2 and 3 are byte-identical.

## Measurement method

The 2026-10-07 run used macOS 15.8 arm64 and the native Fern compiler built from
`4e6f4d29c16920487c4ca4fa4372b58b176834cf`. Compiler SHA256:
`24043bc6354b7f60d8fdd493af8753412bb7e27e162dddebb18a7833beb25a9b`.
Executable SHA256:
`e75d9b9361464a39b9ed8b7c751a8ade6f941fc343971ee3da33e33aab0d9724`.

The harness first completed a five-block capacity pilot in 1.303 seconds, then the
same configurations at 1000 blocks in 2.451 seconds, then five repetitions in 7.202 seconds.
Only block count or repetition count changed at each step. The repeated set has
120 runs: six block sizes, two implementations, two sharing modes, five repetitions.
The delay is 3 samples in this measured set; correctness also covers longer delays.

Timer storage and input are preallocated. There is no warm-up callback. Block
samples time processing only. The outer wall interval also includes checksum
traversals, validation and sample storage, so throughput below describes the
instrumented pipeline. Both sharing modes perform the same output-checksum work:
unique reads before processing, shared retains the old graph and reads afterward.
The harness independently recomputes the stream, all reported checksums and each
percentile. Sample export and sorting happen after the allocation/time marks.

## Results

Each row summarizes five runs of 1000 blocks. Throughput is the median and full
run range in millions of samples/second. Callback mean is the median of per-run
means; each percentile column is the median of the five reported percentiles.
At 1000 samples, p99.9 is the second-largest observation. These tails include the
first callback but are too small a sample for a scheduling guarantee. Clock
quantization is visible at block size 1; individual one-nanosecond differences
should not be interpreted as improvements.

| Block | Sharing | Variant | M samples/s median [range] | Callback mean ns, median | p50 / p95 / p99 / p99.9 ns |
| ---: | --- | --- | ---: | ---: | ---: |
| 1 | unique | direct | 28.470 [22.140, 32.258] | 23.2 | 41 / 42 / 42 / 42 |
| 1 | unique | fip | 16.173 [12.793, 17.454] | 56.7 | 42 / 84 / 84 / 84 |
| 1 | shared | direct | 15.248 [11.194, 16.249] | 51.6 | 42 / 84 / 84 / 125 |
| 1 | shared | fip | 10.531 [8.959, 11.273] | 79.4 | 83 / 84 / 125 / 166 |
| 7 | unique | direct | 56.037 [50.648, 57.673] | 104.4 | 84 / 125 / 125 / 209 |
| 7 | unique | fip | 27.184 [25.910, 29.222] | 239.9 | 250 / 250 / 250 / 333 |
| 7 | shared | direct | 40.649 [38.121, 44.152] | 130.2 | 125 / 167 / 167 / 333 |
| 7 | shared | fip | 23.572 [22.373, 24.684] | 254.6 | 250 / 292 / 292 / 375 |
| 64 | unique | direct | 38.671 [37.668, 40.442] | 1146.9 | 1166 / 1208 / 1375 / 1542 |
| 64 | unique | fip | 27.235 [26.301, 28.424] | 1849.1 | 1834 / 1875 / 1959 / 2458 |
| 64 | shared | direct | 37.852 [36.761, 38.985] | 1189.6 | 1167 / 1250 / 1458 / 1708 |
| 64 | shared | fip | 26.727 [25.321, 28.078] | 1892.3 | 1875 / 1958 / 2000 / 2250 |
| 256 | unique | direct | 35.637 [34.888, 37.006] | 4788.1 | 4750 / 4959 / 6041 / 6291 |
| 256 | unique | fip | 26.857 [25.718, 27.438] | 7164.7 | 7125 / 7333 / 8458 / 9375 |
| 256 | shared | direct | 33.611 [32.398, 36.085] | 5157.8 | 5083 / 5292 / 6167 / 6625 |
| 256 | shared | fip | 26.418 [25.067, 26.782] | 7312.7 | 7250 / 7584 / 8542 / 9542 |
| 1024 | unique | direct | 34.003 [32.295, 34.742] | 20164.8 | 19875 / 22042 / 24875 / 25833 |
| 1024 | unique | fip | 26.528 [25.966, 27.387] | 28735.3 | 28208 / 32334 / 35083 / 36709 |
| 1024 | shared | direct | 33.768 [31.760, 34.443] | 20482.3 | 20208 / 23084 / 26042 / 26375 |
| 1024 | shared | fip | 25.886 [25.508, 26.864] | 29526.4 | 29416 / 31500 / 34875 / 36833 |
| 4096 | unique | direct | 34.787 [33.389, 35.195] | 78540.4 | 77250 / 80834 / 89167 / 95542 |
| 4096 | unique | fip | 26.837 [25.014, 27.142] | 113423.1 | 112667 / 119375 / 124916 / 132250 |
| 4096 | shared | direct | 33.573 [31.627, 34.374] | 82260.1 | 80417 / 97959 / 101250 / 104250 |
| 4096 | shared | fip | 26.611 [25.374, 26.842] | 114906.7 | 114375 / 121042 / 126416 / 139916 |

Both unique implementations made zero allocations, including their first callback,
and had zero fresh heap growth in every run. Both shared implementations made
four allocations per block, including the first. Total shared fresh growth over
1000 blocks was 336/528/1280/5328/20688/82128 bytes for block sizes 1/7/64/256/1024/4096,
identical between implementations. Fresh bytes measure high-water growth under
recycling, not total requested bytes or bytes copied.

The direct loop has higher throughput medians throughout this measured set.
The staged graph walks its buffers several times, while the direct loop combines
the arithmetic in one pass. The source establishes that structural difference;
these timings do not isolate instruction cost, cache behavior or individual
stage costs. No hardware cache counters were collected.

## Process resources and limits

Across all block sizes, startup ranged 500..70458 ns. Whole-process user+system CPU
ranged 0.003831..0.166002 seconds and peak RSS 1130496..1425408 bytes. These include startup,
sample export and sorting, not just callbacks. Major-page-fault outliers reached 23;
page faults are not cache misses. Raw per-run values remain in the results.

The evidence supports bounded storage and allocation-free unique callbacks for
this graph and compiler. It does not establish real-time safety under a host audio
scheduler, general signal quality, NaN/Inf behavior, or a universal FIP speedup.
Keeping simple stages separate is convenient for ownership contracts, but the
ordinary fused loop is the better measured choice for this fixed graph.

## Reproduction

```sh
uv run scripts/bench-dsp.py --compiler /path/to/native/fern \
  --block-sizes 1 7 64 256 1024 4096 --blocks 5 --delay 3 --output /tmp/dsp-pilot
uv run scripts/bench-dsp.py --compiler /path/to/native/fern \
  --block-sizes 1 7 64 256 1024 4096 --blocks 1000 --delay 3 --output /tmp/dsp-scale
uv run scripts/bench-dsp.py --compiler /path/to/native/fern \
  --block-sizes 1 7 64 256 1024 4096 --blocks 1000 --delay 3 --repeats 5 --output /tmp/dsp-repeated
```

Compact results, source hashes, compiler identity and oracle values are in
[`benchmarks/dsp-2026-10-07`](benchmarks/dsp-2026-10-07/metadata.json).
The original full samples/source snapshots are retained at
`/tmp/fern-dsp-main4e6-repeated/`; the harness produces the same evidence layout
for a new run. Metadata records that experiment files were staged on the named
merged compiler revision rather than pretending they were already merged.
