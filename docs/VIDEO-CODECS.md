# Video codecs

Status: initial implementation, 2026-11-07.

The video APIs provide a native lossless codec and an Android platform decoder adapter.
They do not yet constitute a complete video player.

| Format | Native Fern decode | Native Fern encode | Android platform adapter |
| --- | --- | --- | --- |
| QuickTime Animation, QTRLE | RGB24 and ARGB32 packets | RGB24 and ARGB32 packets | No |
| H.264 / AVC | No | No | Yes, device dependent |
| H.265 / HEVC | No | No | Yes, device dependent |
| VP8, VP9, AV1 | No | No | Yes, device dependent |
| MPEG-2, MPEG-4 Part 2, H.263 | No | No | Yes, device dependent |

The Android adapter delegates compressed video decode to MediaCodec.
The platform selects the decoder. Hardware acceleration is not a guarantee.
Codec, profile, level, bit depth, resolution, and HDR support depend on the device.
The adapter does not bundle FFmpeg or another external codec library.
The native QTRLE codec uses Fern on every target.

## Android packet path

`std/android/video` configures an NDK MediaCodec decoder for an existing `ANativeWindow`.
It requires API level 21 or later.
Decoded frames remain in the platform surface, without a Fern pixel copy.
The API exposes input backpressure, output format changes, EOS, flush, and timed output release.
Output format changes report dimensions, crop, and available color identifiers.

Absent color identifiers remain zero. The adapter does not invent a color profile.

`std/video/nal` converts MP4 length prefixes to Annex B start codes.
It reads version-one AVC and HEVC configuration records for Android codec configuration data.
AVC includes SPS, PPS, and optional SPS extensions.

HEVC includes VPS, SPS, PPS, and prefix or suffix SEI arrays.
The conversion preserves NAL payload bytes. It does not validate their coded syntax.
Other HEVC configuration array types return `Unsupported`.

```fern
import "std/video/frame";
import "std/video/nal";
import "std/android/video";

function open_avc(window: usize, format: frame.Format, avcc: [u8]): Result[video.Decoder, frame.VideoError] {
  let limits = frame.default_limits();
  let csd = nal.avc_config(avcc, limits)?;
  return video.open(video.Config {
    codec: video.H264, format: format, csd0: csd.csd0, csd1: csd.csd1,
  }, window, limits);
}
```

Each successful `open` requires exactly one `close`.
Decoder handles and buffer slots must remain with one owner on one thread.
Each input slot permits one successful submission.

Validation failures retain the slot. Platform failures require a flush or a new decoder.
Each output buffer requires one release, including frames that the caller discards.
A flush invalidates outstanding input and output buffers.
A seek requires a keyframe and the required codec configuration.

Input and output timeouts use microseconds.
Timed output release uses an absolute Android monotonic timestamp in nanoseconds.
A packet presentation timestamp is not an absolute release timestamp.
The player must map presentation time to its playback clock.
It must drain output through the EOS flag after input EOS.

This work does not test a real Android device or emulator.
The native ABI test uses a C test double, without a video decoder.
It verifies configuration bytes, cleanup, input capacity, output metadata, EOS, flush, and release calls.
A successful build or ABI test does not establish device codec support or playback performance.

## IVF packets

`std/video/ivf` reads version-zero IVF headers for VP8, VP9, and AV1.
Packet results contain offsets, sizes, timestamps, and the next offset.
The reader does not copy packet payloads.
It can parse a complete packet prefix without the rest of the file.
The header frame count is advisory. Each packet has its own timestamp.

Timestamp conversion uses the declared rational timebase and checked arithmetic.

IVF support does not establish VP8, VP9, or AV1 software decode support.
AV1 temporal units can require further packet handling for a particular platform decoder.
MP4, MOV, Matroska, WebM, MPEG-TS, and fragmented streams still need demuxers.

## Native QTRLE

QuickTime Animation uses literal runs, repeated pixels, and skips into a previous frame.
Its MOV FourCC is `rle `, with a final space.
The codec is simple and lossless, with independent keyframes and inter-frame deltas.
These properties make it a useful uncommon format for this initial implementation.

It predates QOI and has no format relationship to QOI.
It does not provide AV1 or HEVC compression efficiency.

`std/video/qtrle` reads and writes RGB24 and ARGB32 packets.
The container supplies the dimensions, bit depth, and keyframe locations.
Palette formats and RGB555 remain unsupported.

Unknown header flags and size-field flags return `Unsupported`.
The decoder rejects runs outside a row, truncated payloads, invalid skips, and trailing data.
A packet contains its complete size field. The API does not read MOV atoms.

`decode_into` consumes one previous pixel buffer and returns the updated frame.
A unique buffer permits reuse. Retained aliases can cause a copy on update.
The caller must discard the consumed buffer after an error and resume from a keyframe.

The encoder takes an empty previous view for a keyframe.
Otherwise, it emits a delta against one complete previous frame.
An unchanged frame occupies seven bytes.
The encoder uses greedy runs. It does not optimize for minimum packet size.

Pixels use numeric `0xAARRGGBB` values with straight alpha.
RGB24 requires opaque alpha and preserves every RGB sample.
ARGB32 preserves all four channels, including RGB values under transparent alpha.
There is no quantization or inferred color transform.
The native array representation uses eight-byte scalar slots for `u32` values.
The logical limits do not establish a peak RSS limit.

The default limits permit 8,388,608 pixels and a 16 MiB packet.
Encoded output uses the same packet limit.
Dimensions, arithmetic, storage lengths, and limits pass checks before pixel access.
The APIs do not promise recoverable allocator exhaustion.

## Verification

```sh
# Requires the pinned wasmtime and WASI adapter from scripts/toolchain-env.
scripts/test-video

# Requires FFmpeg. Both directions verify independent decoded pixels.
scripts/check-video-interop

# Regenerate vectors with FFmpeg, libx264, and libx265.
python3 scripts/gen-video-fixtures.py

# Measure the fixed QTRLE workload and its allocation count.
scripts/bench-video
```

The checked-in vectors need no external codec during normal native or WASI tests.
Twenty-four FFmpeg packets cover RGB24, ARGB32, alpha, keyframes, partial rows, and unchanged frames.

The widths cross literal, repeat, and skip boundaries.
Fern decodes each packet to the expected ARGB CRC.
FFmpeg also decodes twenty-four Fern packets with exact equality to the original ARGB bytes.
AVC and HEVC configuration and packet conversions preserve FFmpeg decoded pixels in two additional streams.
The malformed-input cases cover packet truncation, runs, skips, dimensions, output budgets, and integer overflow.

`scripts/test-video` executes the native suite with the sanitizer and the WASI suite.
It also builds ARM64 Android shared libraries.
On Linux, it executes the MediaCodec ABI test through a shared library for the host architecture.
The unit workflow runs this script on x86-64 and ARM64 hosts.
The compiler differential suite includes the native video tests.

## Performance checkpoint

The first measurement used x86-64 Linux and the pinned stage0 compiler.
Its SHA-256 is `4698ff2c2123df02546ba2eb419e402018d59db811e75ce236269fd76786f0a3`.
Five trials each decoded 100,000 packets at 521 by 3 pixels.
The sequence contains one keyframe, two changed delta frames, and one unchanged frame.
Each call samples one pixel into a checked sum.
Process startup and fixture setup are included in elapsed time.

The median elapsed time was 0.1778 seconds on the initial host.
The decode loop allocated 200,000 objects, or two per call.
These result allocations remain an optimization target.
This small synthetic workload does not predict full-resolution device playback.
It has no comparison with an established decoder and establishes no world-leading performance claim.

## Player work and qualification order

The common playback priorities are H.264, HEVC, VP9, and AV1, followed by legacy formats.
An Android player also needs demuxers, timestamps, seek indexes, audio synchronization, and surface lifecycle management.
Device tests must cover backpressure, resolution changes, EOS, flush, and playback after a seek.
HDR requires correct metadata, surface configuration, and display support.

Native software decoders for the common formats remain a separate implementation effort.
Their qualification requires official conformance vectors and independent decoded-plane comparisons.
Performance work requires representative clips, fixed compiler versions, and matched decoder settings.
CPU time, peak memory, allocations, frame latency, and device power require separate measurements.
Encoder comparisons require rate-distortion measurements on a shared corpus.
A decoder quality target means correct reconstruction and color metadata, rather than a tunable quality score.
