# ALAC decode measurements, 2026-11-25

This report compares the native ALAC packet decoder with Apple's pinned reference decoder.
It also compares the decoder on main with the decoder of the branch that added this report.
The branch decodes each of the six inputs in less time than Apple's decoder on this host.
The result does not establish ARM64 or Android performance.

## Method

`scripts/bench-alac` builds Apple's encoder and decoder from the pinned sources of `scripts/audio-reference/alac-source.json`.
Apple's encoder writes the packets, and both decoders read the same packets.
Each input has 262,144 frames at 48 kHz in packets of 4,096 frames.
Each input contains two sine waves and a small random signal, or random samples only for `noise2_24`.

Each trial decodes every packet 16 times after four warmup passes.
The timed region excludes the file read, the packet split, and the decoder setup.
A checksum pass after the timed region must agree between Fern and Apple, in WAVE channel order.
Each value is the median of seven trials, in alternate order, on one pinned CPU.

```sh
FERN=bin/fern-selfhost scripts/bench-alac --cpu 3 --trials 7 --rounds 16 --output branch.json
FERN=bin/fern-selfhost scripts/bench-alac --cpu 3 --trials 7 --rounds 16 --stdlib MAIN/internal/stdlib --output main.json
```

The host is an x86-64 Intel Xeon at 2.10 GHz, shared with other work.
Apple's decoder is built with `g++ -O2`. Fern uses `-O` and the compiler of main `6b37aea4`.
The two JSON files in this directory hold every trial, the source hashes, and the binary hashes.

## Results

Times are nanoseconds per decoded sample. The ratio is Fern time divided by Apple time in the same run.

| Input | Main | Main / Apple | Branch | Branch / Apple | Allocations per packet, main → branch |
|---|---:|---:|---:|---:|---:|
| music1_16 | 66.67 | 1.267 | 36.83 | 0.628 | 4 → 3 |
| music2_16 | 60.06 | 1.105 | 36.65 | 0.606 | 7 → 5 |
| music2_24 | 66.21 | 1.132 | 57.69 | 0.853 | 7 → 5 |
| noise2_24 | 17.39 | 3.723 | 5.43 | 0.609 | 1 → 1 |
| music6_24 | 70.78 | 1.171 | 59.14 | 0.827 | 19 → 13 |
| music2_32 | 71.51 | 1.192 | 56.16 | 0.714 | 7 → 5 |

Apple's time for one input changes by up to 30% between the two runs, because the host is shared.
Use the ratios of one run, not the times of different runs.

Callgrind counts the instructions of two decode passes over 65,536 frames.
The counts are exact on repeated runs. They are instructions per decoded sample.

| Input | Main | Branch | Apple |
|---|---:|---:|---:|
| music1_16 | 714.2 | 375.5 | 347.5 |
| music2_16 | 715.7 | 368.4 | 343.3 |
| music2_24 | 758.9 | 437.9 | 385.8 |
| noise2_24 | 288.1 | 54.2 | 73.0 |
| music6_24 | 749.7 | 433.2 | 380.4 |
| music2_32 | 804.4 | 435.5 | 376.2 |

The branch runs about 8–16% more instructions than Apple on compressed packets, and less time.
Apple's encoder writes random samples as raw packets. There the branch runs 26% fewer instructions than Apple.

Peak process memory is 732 KB to 7.3 MB for Fern and 3.8 MB to 8.0 MB for Apple.
It includes the code, all packets in memory, and the output buffer.
The branch does not change it.

## Changes

- `bits.window` reads 64 bits at a bit position with one big-endian load. `bits.read` uses it, and a field no longer takes one pass for each byte.
- `entropy.rice` takes the prefix, the escape, the suffix, and the extra bit from one window, as Apple's `dyn_decomp` does.
- `predict.restore` has a loop for order 4 and a loop for order 8, the two orders that Apple's encoder writes. Each loop keeps its coefficients in locals, as Apple's `unpc_block` does.
- The general predictor loop adapts its coefficients in place, without a call for each sample.
- A raw packet checks its extent once and reads each sample from a window.
- The coefficient array has its final length when it is made, so it does not grow.

The decoder still makes one allocation for each channel element and packet, for the coefficients.

## Limits

These measurements use one x86-64 host. They do not measure native ARM64, Android, power, or thermal behavior.
The inputs are synthetic. Apple's encoder writes orders 4 and 8 only, so the general predictor loop is not in the timed region.
The reference banks of `tests/stdlib/alac_test.fern` check the exact PCM of the general loop and of all other orders.
