# Float PCM playback measurements, 2026-11-18

`Buffered.write_f32` converts normalized float PCM directly into the AAudio ring.
It preserves the signed 16-bit clipping and ties-away rounding contract of `pcm.quantize`.
The method uses binary32 significands and a sequential ring position.
The write path allocates no Fern objects in the targeted allocation test.

## Host result

Nine alternating paired trials each process 10,240,000 samples through a host AAudio mock.
The old path calls `pcm.quantize`, then `Buffered.write`.
The new path calls `Buffered.write_f32`.
Each round includes the same flush calls to make space in the ring.
The measurement excludes stream creation and input construction.

| Path | Median elapsed ns | Allocations per trial |
|---|---:|---:|
| Intermediate PCM array | 91,248,743 | 100,000 |
| Direct conversion | 78,843,685 | 20,000 |

The elapsed time falls by 13.6%. Each trial contains 10,000 rounds of 1,024 samples.
The shared flush operations account for the 20,000 allocations in the direct path.
The test of `write_f32` alone observes zero allocations.

These are host conversion results, not Android playback, acoustic latency, or power measurements.
The initial float64 conversion removed allocations but increased elapsed time.
That experiment did not justify a speed claim. The final implementation uses exact integer significand conversion instead.

## Correctness

Normal and sanitized mock tests compare output against the float64 quantizer for 198,648 finite samples.
These include every signed quantization threshold and the adjacent binary32 values.
The tests also sample every finite exponent, subnormal values, extreme amplitudes, and both signs.

Other cases cover NaN, infinities, signed zero, clipping, partial writes, and ring wrap.
A failed conversion publishes no samples. Non-finite values outside the accepted prefix do not fail the write.
The callback consumes samples on its own mock audio thread.

The tests compile for Android ARM64.
The integer playback API and Tune effect pipeline remain unchanged.

## Reproduce

```sh
mkdir -p build/audio-fused
cc -shared -fPIC -Wall -Wextra -Werror -pthread \
  -o build/audio-fused/libaaudio.so scripts/audio-reference/aaudio-buffered-mock.c
bin/fern-selfhost -target x86-64-android -shared -export main \
  -o build/audio-fused/benchmark.so bench/audio/android_buffered.fern internal/stdlib
LD_LIBRARY_PATH="$PWD/build/audio-fused" python3 -c \
  'import ctypes; assert ctypes.CDLL("./build/audio-fused/benchmark.so").main() == 0'
```

Use `arm64-android` on an ARM64 Linux host.
The JSON report records all trial values and source hashes.
