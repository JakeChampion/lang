# QOA frame decode latency, 2026-11-27

This report adds the time of each frame decode to the QOA comparison of the [2026-11-26 report](../qoa-decode-2026-11-26/README.md).
At the 99th percentile, Fern takes 1.8 to 2.8 times the reference time for one frame.
The slowest Fern frame at the 99th percentile takes 0.14 ms. One QOA frame holds 106.7 ms of audio at 48 kHz.
The result does not establish ARM64 or Android latency.

## Method

`scripts/bench-audio` uses the method of the 2026-11-26 report for throughput, allocations, and peak RSS.
After the throughput region, each decoder does a second pass of the same number of rounds.
That pass reads the monotonic clock before and after each frame decode.
It keeps each time in an array that it made before the pass.

Each decoder sorts the times and writes the nearest-rank 50th, 90th, and 99th percentiles and the maximum.
The script gives the median of each percentile across the trials, and the maximum across all trials.
The second pass is outside the throughput region, so the throughput and allocation results do not include it.

```sh
scripts/bench-audio --cpu 3 --trials 7 --rounds 64 --output x86-64.json
```

Each trial times 64 rounds of 52 frames, which is 3,328 frames.
The host is an x86-64 Intel Xeon at 2.10 GHz with four CPUs, shared with other work.
Fern uses `-O` and the stage0 compiler of `bootstrap/stage0.lock`, `84426d4a`.
The JSON file holds every trial, the source hashes, and the binary hashes.

## Results

Times are microseconds for the decode of one frame of 5,120 sample frames. Each cell is Fern / C.

| Input | Channels | p50 | p90 | p99 | Maximum |
|---|---:|---:|---:|---:|---:|
| silence | 2 | 71.0 / 30.7 | 81.6 / 33.8 | 105.3 / 48.8 | 947 / 440 |
| impulse | 2 | 73.9 / 30.8 | 100.2 / 35.2 | 141.4 / 49.7 | 1,221 / 760 |
| speech | 1 | 37.3 / 16.5 | 49.3 / 16.5 | 75.9 / 29.7 | 2,966 / 762 |
| music | 2 | 63.7 / 27.0 | 78.1 / 31.1 | 96.0 / 52.7 | 741 / 530 |
| noise | 2 | 74.2 / 30.9 | 85.5 / 33.6 | 95.1 / 46.2 | 2,272 / 500 |

In this run, Fern takes 7.22 to 7.90 ns for each sample. C takes 2.77 to 3.34 ns.
The ratio is 2.37 to 2.60. This host is slower and has more noise than the host of the 2026-11-26 report.

The ratio between p90 and p50 is larger for Fern than for C on all five inputs.
The maximum values come from other work on the shared host, and they are different in each run.
Do not compare the maximum values between the two decoders.

## Remaining work

Issue [#14515](https://github.com/JakeChampion/lang/issues/14515) owns these measurements:

- Native ARM64. The `audio-pair` job of `.github/workflows/perf.yml` runs this script on `ubuntu-24.04-arm`.
- A host with no other work, for the maximum values.
- Recorded speech and music corpora in place of synthetic signals.
- Android device time, underruns, battery use, and thermal conditions, which need the harness of [#14514](https://github.com/JakeChampion/lang/issues/14514).
