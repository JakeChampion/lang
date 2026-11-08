# Audio codecs

Status: [reference] — Initial implementation, 2026-11-07.

The codecs use Fern code on every target. They need no C library, process, or platform codec at runtime.
The Opus packet API reuses the Fern CELT implementation.

| Format | Decode | Encode | Current scope |
| --- | --- | --- | --- |
| WAVE | Yes | Yes | Integer PCM, 8/16/24/32 bits, one through eight channels. Extensible PCM uses standard channel masks and matching valid and container depths. |
| FLAC | Yes | No | Native FLAC, 4 through 32 bits, one through eight channels. All predictor types, Rice/Rice2, wasted bits, and stereo assignments. |
| QOA | Yes | Yes | Static files, signed 16-bit PCM, one through eight channels. Independent frame APIs also accept individual frames. |
| ULC2 | Yes | Yes | Example library under `examples/ulc`, with block APIs, transient windows, and VBR/CBR/ABR encoding. |
| Opus | Partial | Partial | Standard CELT-only packets. The encoder uses 48 kHz fullband mono or stereo. The decoder accepts all CELT bandwidths and Opus output rates. |
| MP3 | Yes | No | Indexed-bitrate MPEG-1/2/2.5 Layer III, mono/stereo, long/short/mixed blocks, bit reservoir, and recognized Xing/LAME gapless metadata. |
| AAC, Vorbis, ALAC, WavPack | No | No | No implementation in these modules. |

QOA is Quite OK Audio, the audio counterpart of QOI.
It uses approximately 3.2 bits per sample plus frame headers.
The codec favors simple, fast decode over the compression efficiency of Opus.
It is lossy. FLAC and integer PCM WAVE preserve every sample.

The [ULC2 example library](../examples/ulc/README.md) has its own block API and WAV tools.
Its documentation includes reference checks and a separate performance report.

## Use

```fern
import "std/audio/pcm";
import "std/audio/flac";
import "std/audio/wav";

function convert(input: [u8]): Result[u8[], pcm.AudioError] {
  let limits = pcm.default_limits();
  let block = flac.decode(input, limits)?;
  return wav.encode(block.samples, block.format, limits);
}
```

`examples/audio-convert.fern` converts MP3, WAVE, native FLAC, or QOA input to WAVE or QOA output.
Integer input retains its bit depth. QOA output from integer input requires 16-bit samples.
MP3 input uses float PCM. The example quantizes it to 32-bit WAVE or 16-bit QOA.

The example does not preserve container tags or custom channel layout metadata.
The convenience example uses the default allocation limits. It does not implement a complete player.

```sh
fern -target x86-64-linux -o audio-convert examples/audio-convert.fern
./audio-convert wav input.flac output.wav
./audio-convert wav input.mp3 output.wav
./audio-convert qoa input16.wav output.qoa
```

## PCM and memory

`std/audio/pcm.Format` declares the sample rate, channel count, and bit depth.
Integer samples are interleaved, signed, right-aligned `i32` values.
For example, a 24-bit sample retains its full signed 24-bit value.
A frame contains one sample per channel.

`pcm.quantize` converts normalized `f32` samples to the declared integer bit depth.
It clips to the integer range, rounds ties away from zero, and rejects non-finite values.
It does not add dither.

`pcm.Limits.max_samples` counts samples across all channels.
The default permits 1,048,576 output samples and 16 MiB of encoded input.

The encoders also use `max_input_bytes` as their encoded output budget.
The decoder checks output counts before allocation. FLAC also checks the cumulative count when STREAMINFO omits the total.
Limits bound output storage. They do not specify a peak RSS budget.

Use frame APIs for long tracks:

- `qoa.decode_frame_into` consumes a reusable `i32[]` output buffer. A frame needs at most `5120 * channels` entries.
- `flac.decode_frame_into` consumes a reusable output buffer. STREAMINFO supplies the maximum block size, at most 65,535 frames.
- `mp3.Decoder.decode_frame_into` consumes a reusable `f32[]` buffer of at most 2304 required entries. The decoder retains bounded history between frames.
- Each result declares its valid frame count and consumed byte count. Buffer entries after the valid samples retain their previous values.
- QOA frame state permits independent decode and seek. FLAC frame decode uses the format from STREAMINFO.
- FLAC still allocates bounded channel scratch space for each frame. These APIs do not promise zero allocations.

The frame APIs need one complete encoded frame. They can return `Truncated` when a caller supplies an incomplete frame.
They do not own a file reader or retain encoded input between calls.
The WAVE range API borrows a complete WAVE byte view and decodes only the requested sample range.

QOA verifies static frame lengths, byte counts, format consistency, and total samples.
FLAC verifies CRC-8, CRC-16, sample bounds, frame sequence, and alignment bits.
Whole-stream FLAC decode verifies a supplied MD5. Individual frame calls cannot verify a complete stream digest.

## MP3

`std/audio/mp3` provides native Layer III decode with normalized float output.
Samples can exceed the device range. Apply gain or `pcm.quantize` at the conversion boundary.
The decoder supports indexed bitrates in MPEG-1, MPEG-2, and MPEG-2.5 streams.
It rejects Layer I, Layer II, and free-format bitrate headers.

`decode_raw` retains all coded samples and codec delay.
`decode` skips a Xing or Info metadata frame and applies recognized LAME delay and padding.
`inspect_track` exposes those boundaries. It skips ID3v2.2 through ID3v2.4, ID3v1, and bounded trailing APE tags.
The module does not expose tag contents or build a seek index.

Zero-padding stream-copy tags are valid. Adjusted padding has a minimum of zero.

The whole-file APIs require a constant output format.
`decode` validates the frame chain and raw sample budget before output allocation.
`decode_raw` checks cumulative limits at each frame boundary.
The raw budget includes samples that gapless metadata will remove.

The frame API accepts a complete frame at a known byte offset.
It reports the sample rate, channel count, and consumed byte count.
A sample-rate or MPEG-version change resets state. Channel-mode changes retain state.

A result with `needs_history` consumes the frame and retains reservoir data but supplies zero valid PCM frames.
Use that result for decoder warmup after a seek. A decode error resets decoder state.

Decoder history contains at most 511 reservoir bytes, 576 overlap floats, and 960 synthesis floats.
Frame decode still allocates bounded scratch arrays. It is not an allocation-free audio callback.
The frame API does not scan damaged input for synchronization.

## Opus and Android

`std/opus` provides stateful CELT packet encode and decode.
The packet API handles multi-frame packets and padding.
It returns `Unsupported` for SILK and hybrid packets.

The SILK implementation is in progress under [#14510](https://github.com/JakeChampion/lang/issues/14510).
`std/opus/silk/math` supplies fixed-point arithmetic. `std/opus/silk/predict` reconstructs stable prediction filters.
`std/opus/silk/entropy` decodes frame parameters. `std/opus/silk/core` reconstructs integer PCM for one coded channel.

The entropy and predictor tables come from pinned Pion Opus sources.
`std/opus/silk` decodes internal-rate packets, including stereo and redundant frames.
`std/opus/silk/resample` converts one channel to each Opus output rate and retains the reference input delay.
The public Opus facade does not yet call these modules.

The reference tests cover signed arithmetic boundaries, division, saturation, filter stability, and spectral frequency spacing.
They compare exact integer results with Pion Opus revision `e7b8aa55508cfd30a968dbc695d4272a8e04f2f2`.
The prediction corpus includes 64 narrowband and wideband filters and nine separate stability cases.
Regenerate the tables and vectors with a clone of that revision:

```sh
make selfhost-cli
python3 scripts/gen-silk-tables.py --source-dir /path/to/pion-opus/internal/silk
python3 scripts/gen-silk-math-vectors.py --source-dir /path/to/pion-opus/internal/silk
python3 scripts/gen-silk-predict-vectors.py --source-dir /path/to/pion-opus
python3 scripts/gen-silk-frame-vectors.py --source-dir /path/to/pion-opus
python3 scripts/gen-silk-packet-vectors.py --source-dir /path/to/pion-opus
python3 scripts/gen-silk-resample-vectors.py --source-dir /path/to/pion-opus
```

The table and arithmetic generators verify source hashes. The prediction generator reads the pinned Git objects.
The generated test vectors need no Go runtime or network access when tests run.
The frame corpus compares all decoded parameters, final range, and PCM with 36 sequential Pion reference frames.
It covers all three internal sample rates, both frame durations, and changes in predictor history.
A separate frame matches the pinned libopus parameters and PCM exactly.

The packet corpus compares final range and complete PCM for 24 sequential packets with the pinned Pion reference.
It covers stereo, redundant frames, 10/20/40/60 ms packets, channel conversion, and internal-rate changes.

The resampler corpus checks all 15 rate pairs with saturation, impulses, ramps, noise, and continuous delay history.
Each case compares a checksum of all output samples and explicit boundary samples.
A separate libopus frame verifies the input delay.

It does not read Ogg containers, apply pre-skip or gain, or trim the final packet.
Arbitrary `.opus` file playback therefore needs more work.

Opus samples use interleaved `f32` values.
The encoder accepts finite values between -1 and 1.
Decoder output can exceed that interval. A device conversion must apply clipping.
The CELT encoder has a 120-sample delay at 48 kHz.
A container must account for the delay and final padding.

These modules compile for `arm64-android`, as shared libraries.
The tests verify that build. They do not measure an Android device or its audio callback.
The `std/android/audio` stream currently accepts mono, signed 16-bit samples stored in `i32` values.
Stereo playback, device format negotiation, resampling, seek indexes, gapless playback, and callback scheduling need separate player work.
A player must also handle partial device writes.

## Verification

```sh
# Requires the pinned wasmtime and WASI adapter from scripts/toolchain-env.
scripts/test-audio

# Requires libopus. Compares every sample of 24 packets with the system decoder.
scripts/check-audio-interop

# Requires cc, FFmpeg, and network access to pinned reference sources.
scripts/check-mp3-interop --iso
python3 scripts/gen-mp3-vectors.py
scripts/bench-mp3

# Offline table checks use the exact pinned source file.
python3 scripts/gen-mp3-tables.py --check --source /path/to/minimp3.h
python3 -m unittest discover -s scripts -p test_gen_mp3_tables.py

# Requires cc, FFmpeg, and network access to the pinned QOA source.
python3 scripts/gen-audio-fixtures.py

# Requires cc and network access to the pinned QOA source.
scripts/bench-audio
```

The checked-in vectors need no external decoder during normal tests.
The generator independently verifies its synthetic FLAC vectors with FFmpeg.
It also produces FLAC vectors through FFmpeg and QOA vectors through the pinned C reference.
The vectors cover frame boundaries, signed extrema, predictor orders, residual methods, and stereo reconstruction.
The suites also cover truncated input, invalid headers, checksums, forbidden subframes, and allocation limits.
The standard compiler differential suite includes the audio, Opus, and MP3 test programs.

The initial local checks also decoded 36 WAVE files and 54 FFmpeg FLAC files with exact PCM equality.
Nine additional QOA files matched the C encoder byte for byte.
The native sanitizer reported zero live bytes after both suites.
The audio, Opus, and MP3 suites passed under WASI and built as ARM64 Android shared libraries.

System libopus 1.5.2 decoded the 24 interoperability packets with a maximum absolute difference of 0.00005078.
The largest RMS difference was 0.00001836.
This comparison uses a tolerance. It is not a claim of bit-exact output or complete Opus conformance.
The CELT tests retain their separate libopus reference checks.

The MP3 suite has 16 golden vectors from a pinned scalar minimp3 reference.
They cover all nine sample rates, bit reservoir history, mixed blocks, intensity stereo, gapless output, stream-copy tags, and protected frames.
An additional 8 kHz mixed-block vector compares every sample with FFmpeg.
It verifies scalefactor and Huffman region boundaries independently of the scalar reference.
The tests also cover metadata, private bits, buffer reuse, malformed input, and reservoir warmup.
The table generator tests verify the implicit zero padding of C array rows.

`scripts/check-mp3-interop --iso` checks 90 generated files and 14 pinned conformance streams.
All 90 files match scalar minimp3 float output bit for bit, including gapless output.
Their FFmpeg comparison has a maximum absolute sample difference of 0.00000238.
The script also checks 36 files with distinct Huffman tables across switched regions.
They cover all nine sample rates and long, short, and mixed transitions against FFmpeg.

The 14 conformance streams also match scalar minimp3 float output bit for bit.
Integer conversion differs from the supplied ISO PCM by at most one integer step.
Each stream exceeds the 96 dB PSNR criterion. The lowest measured result is 109.81 dB.

Some ISO PCM files omit the final encoded frame. The test compares every supplied reference sample.
The complete output still has a separate scalar reference check.

FFmpeg differs from the ISO reference on two MPEG-2 intensity-stereo vectors.
Those cases use the ISO PCM reference as their quality oracle.
These tests establish the measured conformance scope. They do not establish complete support for every MP3 file.

## Performance baseline

The first whole-file QOA benchmark used x86-64 Linux and the pinned stage0 compiler from `bootstrap/stage0.lock`.
Each process decoded a 10,004-frame stereo file 1,000 times.
Five trials alternated Fern and C process order and checked equal output sums.

| Decoder | Median elapsed time |
| --- | ---: |
| Fern | 0.1691 s |
| QOA C reference, `cc -O3 -fwrapv` | 0.06444 s |

Fern took approximately 2.62 times the reference time on this workload.
Fern writes `i32` samples and validates the static frame chain. The reference writes 16-bit samples.

The benchmark includes process startup and allocation. It does not isolate the decode kernel.
Use `scripts/bench-audio` to reproduce the comparison. Its report identifies the compiler by SHA-256.

The MP3 benchmark uses a one-second, 48 kHz stereo track and normalized float output.
Each process decodes the complete track 100 times. Five trials alternate implementation order.
The C builds use `cc -O3 -ffp-contract=off` and the pinned minimp3 source.

| MP3 decoder | Median elapsed time |
| --- | ---: |
| Fern with four-lane DCT and synthesis | 0.7134 s |
| C with explicit SIMD disabled | 0.1063 s |
| C with SIMD enabled | 0.07454 s |

The first scalar Fern implementation took 1.374 s on this workload.
The vector kernels reduce that time by approximately 48 percent and retain the scalar reference PCM bits.

The current result remains approximately 6.7 times the scalar C time and 9.6 times the SIMD C time.

Use `scripts/bench-mp3` to reproduce the comparison. Its report records the input and compiler SHA-256 values.
These timings include allocation and process startup. They are local baselines, not Android device measurements.

These measurements do not establish the best speed, memory use, or perceptual quality.
The performance goal requires improvements against measured reference implementations.
Quality comparisons also require speech and music corpora, listening tests, and equal bitrate and latency settings.
Android qualification requires device measurements of decode time, allocations, battery use, and underruns.

## Remaining codec work

A complete player still needs complete Opus decode and Ogg demux.
Further MP3 work includes free-format streams, damaged-stream synchronization, more tag formats, and a sample-accurate seek index.
Opus qualification must cover SILK, hybrid mode changes, packet loss, Ogg granule positions, pre-skip, gain, and end trimming.
Additional formats need their own reference vectors and benchmarks before the support matrix changes.
No metadata inspector or unsupported-format placeholder counts as a codec implementation.
