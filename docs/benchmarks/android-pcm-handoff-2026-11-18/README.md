# Borrowed PCM handoff on the host

`scripts/bench-audio-playback` compares the borrowed PCM helper with the current `Music.write` repack on the same host.
Each write accepts 480 stereo frames at 48 kHz, with signed 16-bit PCM.
Both paths submit the same bytes to the same AAudio ABI mock.
The baseline already holds an `i32` array, so the comparison excludes the cost to create that array from codec output.

The benchmark runs 100 warmup calls for each path.
It then alternates the order of ten paired trials, with 100,000 calls per trial.
The mock checks every submitted byte and the accepted frame count.
The borrowed path also checks that AAudio receives the codec pointer at the correct frame offset.

The allocation count covers the measured calls, after setup.
The borrowed path consumes each `Result` immediately through a pattern or `unwrap_or`.
Other result consumers can require allocations.
The baseline allocates a 1,920-byte PCM temporary for each write.
The borrowed path uses the decoder's current scratch word for bounds checks and creates no PCM array.

## Results

Three separate processes each repeat the paired trial sequence.
The table combines 30 measured trials for each path.
The raw records and exact compiler and input identities are in [results.json](results.json).

| Host path | Allocations per write | Median nanoseconds per write |
| --- | ---: | ---: |
| `Music.write` repack | 1 | 1136.89 |
| Borrowed PCM | 0 | 78.44 |

These host times exclude real decoder and audio-device work.

## Validation

`scripts/test-audio-playback` builds an ARM64 Android library without device execution.
It executes host Android ABI libraries in normal and Fern sanitizer modes.
The controls cover mono and stereo, partial writes, backpressure, platform errors, frame offsets, format mismatches, EOS, flush, and exact buffer release.
The mock retains the codec buffer throughout each synchronous write.
It captures all 65,536 signed 16-bit values and compares the bytes with an independent Python reference.

Independent FFmpeg 7.1.5 and 6.1.1 WAVE round trips preserve all captured PCM bytes.
These checks verify PCM transport. The mock does not decode compressed audio.
The complete `scripts/test-video` gate also exercises the codec view APIs and portable native/WASI controls.

## Limits

These measurements cover the host handoff and PCM repack.
They do not measure Android hardware decode, device throughput, platform internal copies, power, or acoustic synchronization.
Hardware decode callers must select `HardwareOnly` and record the selected codec and supported input for device measurements.
Plex retains its Media3 A/V path.
