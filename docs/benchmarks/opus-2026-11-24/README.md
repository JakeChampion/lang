# Opus voice measurements, 2026-11-24

`std/opus` encodes 21.0 times slower than libopus 1.4 on this x86-64 host.
It decodes 10.7 times slower.
Both are faster than real time: the encoder is at 18.6 times and the decoder at 102 times.
This result does not show the best speed in the world.

## Case

The case is the voice path of the walkie-talkie (#14080): 48 kHz mono, 20 ms frames, 24 kbit/s, VBR.
The input is 10 seconds of synthetic speech from `scripts/bench-opus`.
It contains voiced harmonics with vibrato, a noise fricative, a pause, and a DC offset of 0.02.

libopus uses `OPUS_APPLICATION_RESTRICTED_LOWDELAY`, which selects CELT only.
It uses complexity 5, which is the complexity of the `std/opus` encoder.
Both decoders decode the same libopus packet stream.

Each program times its codec loop with a monotonic clock.
The time does not include process start, file reads, or the division of the input into frames.
Each run codes the input 10 times, for 100 seconds of audio.
The table gives the median of 7 trials. The trials alternate the order of the two programs.

| Operation | `std/opus` | libopus 1.4 | Ratio |
|---|---:|---:|---:|
| Encode | 5.386 s | 0.257 s | 21.0 |
| Decode | 0.983 s | 0.092 s | 10.7 |

## Agreement

The `std/opus` decoder output differs from libopus by 0.00000016 at most.
Every final entropy range is the same.

478 of the 500 encoded packets are the same as the libopus packets, byte for byte.
The other 22 packets have the same length, and they differ only after their first bytes.
The encoder state converges again after each of them.
The system libopus selects SIMD kernels at run time, but the CELT encoder in `std/opus` follows scalar libopus.
This is a possible cause, but this measurement does not prove it.
The proof needs the pinned scalar build, and its archive was not available to this host.

Before this change, only 20 of 100 packets were the same on a similar input.
The `std/opus` encoder did not remove DC before CELT, but `opus_encode` in libopus does.

## Host

- Intel Xeon at 2.10 GHz, 4 cores, Linux 6.18, x86-64 build of the stage0 compiler.
- libopus 1.4 from the Ubuntu package `libopus0` 1.4-1build1.
- `result.json` has each trial, the compiler SHA-256, and the libopus SHA-256.

## Repeat

```sh
scripts/bench-opus > result.json
```

The script needs libopus, `cc`, and the stage0 compiler. It does not need the libopus headers.
