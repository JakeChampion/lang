# Audio codecs

Status: initial implementation, 2026-11-07.

The codecs use Fern code on every target. They need no C library, process, or platform codec at runtime.
The Opus packet API reuses the Fern CELT implementation.

| Format | Decode | Encode | Current scope |
| --- | --- | --- | --- |
| WAVE | Yes | Yes | Integer PCM, 8/16/24/32 bits, one through eight channels. Extensible PCM uses standard channel masks and matching valid and container depths. |
| FLAC | Yes | No | Native FLAC, 4 through 32 bits, one through eight channels. All predictor types, Rice/Rice2, wasted bits, and stereo assignments. |
| QOA | Yes | Yes | Static files, signed 16-bit PCM, one through eight channels. Independent frame APIs also accept individual frames. |
| Opus | Partial | Partial | Standard CELT-only packets. The encoder uses 48 kHz fullband mono or stereo. The decoder accepts all CELT bandwidths and Opus output rates. |
| MP3 | No | No | Requires a Layer III decoder and independent conformance vectors. |
| AAC, Vorbis, ALAC, WavPack | No | No | No implementation in these modules. |

QOA is Quite OK Audio, the audio counterpart of QOI.
It uses approximately 3.2 bits per sample plus frame headers.
The codec favors simple, fast decode over the compression efficiency of Opus.
It is lossy. FLAC and integer PCM WAVE preserve every sample.

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

`examples/audio-convert.fern` converts WAVE, native FLAC, or QOA input to WAVE or QOA output.
QOA output requires 16-bit input. The example does not discard precision to meet that requirement.
The example does not preserve container tags or custom channel layout metadata.
The convenience example uses the default allocation limits. It does not implement a complete player.

```sh
fern -target x86-64-linux -o audio-convert examples/audio-convert.fern
./audio-convert wav input.flac output.wav
./audio-convert qoa input16.wav output.qoa
```

## PCM and memory

`std/audio/pcm.Format` declares the sample rate, channel count, and bit depth.
Integer samples are interleaved, signed, right-aligned `i32` values.
For example, a 24-bit sample retains its full signed 24-bit value.
A frame contains one sample per channel.

`pcm.Limits.max_samples` counts samples across all channels.
The default permits 1,048,576 output samples and 16 MiB of encoded input.

The encoders also use `max_input_bytes` as their encoded output budget.
The decoder checks output counts before allocation. FLAC also checks the cumulative count when STREAMINFO omits the total.
Limits bound output storage. They do not specify a peak RSS budget.

Use frame APIs for long tracks:

- `qoa.decode_frame_into` consumes a reusable `i32[]` output buffer. A frame needs at most `5120 * channels` entries.
- `flac.decode_frame_into` consumes a reusable output buffer. STREAMINFO supplies the maximum block size, at most 65,535 frames.
- Each result declares its valid frame count and consumed byte count. Buffer entries after the valid samples retain their previous values.
- QOA frame state permits independent decode and seek. FLAC frame decode uses the format from STREAMINFO.
- FLAC still allocates bounded channel scratch space for each frame. These APIs do not promise zero allocations.

The frame APIs need one complete encoded frame. They can return `Truncated` when a caller supplies an incomplete frame.
They do not own a file reader or retain encoded input between calls.
The WAVE range API borrows a complete WAVE byte view and decodes only the requested sample range.

QOA verifies static frame lengths, byte counts, format consistency, and total samples.
FLAC verifies CRC-8, CRC-16, sample bounds, frame sequence, and alignment bits.
Whole-stream FLAC decode verifies a supplied MD5. Individual frame calls cannot verify a complete stream digest.

## Opus and Android

`std/opus` provides stateful CELT packet encode and decode.
The packet API handles multi-frame packets and padding.
It returns `Unsupported` for SILK and hybrid packets.
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
The standard compiler differential suite includes both new test programs.

The initial local checks also decoded 36 WAVE files and 54 FFmpeg FLAC files with exact PCM equality.
Nine additional QOA files matched the C encoder byte for byte.
The native sanitizer reported zero live bytes after both suites.
Both suites passed under WASI and built as ARM64 Android shared libraries.

System libopus 1.5.2 decoded the 24 interoperability packets with a maximum absolute difference of 0.00005078.
The largest RMS difference was 0.00001836.
This comparison uses a tolerance. It is not a claim of bit-exact output or complete Opus conformance.
The CELT tests retain their separate libopus reference checks.

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

These measurements do not establish the best speed, memory use, or perceptual quality.
The performance goal requires improvements against measured reference implementations.
Quality comparisons also require speech and music corpora, listening tests, and equal bitrate and latency settings.
Android qualification requires device measurements of decode time, allocations, battery use, and underruns.

## Remaining codec work

A complete player needs MP3 Layer III decode, complete Opus decode, and Ogg demux first.
MP3 qualification must cover the bit reservoir, ID3, Xing/LAME delay, padding, malformed frames, and seek boundaries.
Opus qualification must cover SILK, hybrid mode changes, packet loss, Ogg granule positions, pre-skip, gain, and end trimming.
Additional formats need their own reference vectors and benchmarks before the support matrix changes.
No metadata inspector or unsupported-format placeholder counts as a codec implementation.
