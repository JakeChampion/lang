# QOA decode measurements, 2026-11-26

This report compares the native QOA frame decoder with the pinned C reference decoder.
It replaces the first QOA baseline, which timed complete processes on a two-frame file.
Fern takes 2.12 to 2.40 times the reference time on each of the five inputs.
The result does not establish ARM64 or Android performance.

## Method

`scripts/bench-audio` builds the pinned `qoa.h` from `scripts/gen-audio-fixtures.py` with `cc -O3 -fwrapv`.
The reference encoder writes each file, and both decoders read the same file.
Each input has 262,144 frames at 48 kHz, which is 52 QOA frames.

| Input | Channels | Signal |
|---|---:|---|
| silence | 2 | Zero samples |
| impulse | 2 | A full-scale click every 4,800 frames, with alternate signs |
| speech | 1 | Three formants at a 4 Hz syllable rate, with a pause in each phrase, and a small random signal |
| music | 2 | Two alternating three-note chords with three harmonics, and a small random signal |
| noise | 2 | Full-scale random samples |

The signals are synthetic. The JSON file records the SHA-256 of each WAVE and QOA file.

Each trial decodes every frame 64 times after four warmup passes.
Both decoders write each frame into one reusable buffer.
The timed region excludes the process start, the file read, and the header read.
A checksum of all samples after the timed region must agree between Fern and C.
Each value is the median of seven trials, in alternate order, on one pinned CPU.

```sh
scripts/bench-audio --cpu 0 --trials 7 --rounds 64 --output x86-64.json
```

The host is an x86-64 Intel Xeon at 2.30 GHz, shared with other work.
Fern uses `-O` and the stage0 compiler of `bootstrap/stage0.lock`, `84426d4a`.
The JSON file holds every trial, the source hashes, and the binary hashes.

## Results

Times are nanoseconds per decoded sample. The ratio is Fern time divided by C time in the same run.

| Input | Fern | C | Fern / C | Fern allocations per frame | Peak RSS, Fern / C |
|---|---:|---:|---:|---:|---:|
| silence | 6.43 | 2.77 | 2.32 | 3 | 352 / 2,000 KiB |
| impulse | 6.71 | 2.81 | 2.38 | 3 | 356 / 2,000 KiB |
| speech | 6.38 | 3.00 | 2.12 | 2 | 232 / 1,884 KiB |
| music | 6.43 | 2.79 | 2.30 | 3 | 356 / 2,000 KiB |
| noise | 6.41 | 2.67 | 2.40 | 3 | 352 / 2,000 KiB |

The decode time does not change with the signal for either decoder.
Fern writes `i32` samples and C writes 16-bit samples.

Fern makes one allocation for each frame and one more for each channel.
The allocations come from the array of LMS states, one record for each channel.
The free list supplies all of them, so the fresh heap bytes in the timed region are zero.
The compiler gives each element of an array of a scalar record its own box.
Issue [#17215](https://github.com/JakeChampion/lang/issues/17215) owns that cost.

The peak RSS includes code, input, and output.
The Fern binary is static. The C binary links the C library dynamically, which accounts for most of its peak.

## Remaining work

Issue [#14515](https://github.com/JakeChampion/lang/issues/14515) owns these measurements:

- Native ARM64. The `audio-pair` job of `.github/workflows/perf.yml` runs this script on `ubuntu-24.04-arm`.
- Recorded speech and music corpora in place of synthetic signals.
- Latency distributions for each frame. The [2026-11-27 report](../qoa-frame-latency-2026-11-27/README.md) adds them.
- Android device time, underruns, battery use, and thermal conditions, which need the harness of [#14514](https://github.com/JakeChampion/lang/issues/14514).
