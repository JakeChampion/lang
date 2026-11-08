# Video codecs

Status: [reference] native packets and Android media paths, 2026-11-08.

The video APIs provide a native lossless codec and Android platform codec adapters.
They do not yet constitute a complete video player.

| Format | Native Fern decode | Native Fern encode | Android platform decode and encode |
| --- | --- | --- | --- |
| QuickTime Animation, QTRLE | RGB24 and ARGB32 packets | RGB24 and ARGB32 packets | No |
| H.264 / AVC | No | No | Yes, device dependent |
| H.265 / HEVC | No | No | Yes, device dependent |
| VP8, VP9, AV1 | No | No | Yes, device dependent |
| MPEG-2, MPEG-4 Part 2, H.263 | No | No | Yes, device dependent |

The Android adapter delegates compressed video decode and encode to MediaCodec.
The capabilities module queries support for each complete request.
Selection can prefer hardware, require hardware, require software, or preserve platform order.
Hardware and software attributes come from Android API 29, without codec-name guesses.
Earlier API levels report an unknown implementation.

A supported format can still fail configuration because of device resources or codec defects.
Codec, profile, level, bit depth, resolution, and HDR support depend on the device.
The adapter does not bundle FFmpeg or another external codec library.

The native QTRLE codec uses Fern on every target.

## Android player paths

`std/android/player` provides complete platform A/V playback through Android `MediaPlayer`.
It supports the platform containers and streams, with audio, video, pause, seek, and completion callbacks.
The platform selects codecs and controls synchronization.
Hardware use, format support, and stream support depend on the device.
This path is available for an app that needs audio and video together.

The platform player opens paths, file URIs, HTTP streams, and granted content URIs.
Content sources use the activity context and Android's content resolver.
The app must hold the URI read grant from the document picker or MediaStore.
This avoids a dependency on unrestricted filesystem paths or broad storage permission.
The caller must preserve the read grant for later source opens.

`std/android/video/player` provides explicit video codec policies and buffer control.
It controls one video track and does not synchronize an audio track.
The native Fern packet codecs remain independent of both platform players.
An app can choose the path that supplies its required controls.

The platform player exposes a media-time and monotonic-time anchor through `timestamp`.
It returns `None` before an anchor is available.
The anchor is a platform estimate, without the clock rate or a physical output latency measurement.
Each operation bounds its JNI references, so repeated position queries do not retain local Java references.
A failed construction releases partial resources.
Seek requests use the closest-frame mode and reject values above Android's signed 32-bit millisecond limit.

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

## Android discovery, export, and playback

`std/android/video/capabilities` queries regular codecs through an attached JNI environment.
Regular codecs exclude implementations that require secure or tunneled operation.
Requests contain MIME, dimensions, direction, frame rate, bitrate, profile, level, and color format.
Zero optional fields mean unknown or unspecified, rather than universal support.
The result includes profile-level pairs, color formats, vendor attributes, and aliases.
Candidate selection checks the exact request and excludes aliases and duplicate names.

`open_selected` and `select_encoder` attempt candidates in policy order.
They record each configuration failure and close the failed codec before the next attempt.
`HardwareOnly` never silently selects software.
`fallback_to_software` closes a failed decoder before it opens a replacement.
The caller must restart at a sync sample with codec configuration.

Android software codecs supply this fallback. Fern does not bundle a common-format software decoder.

Byte-buffer encoders accept tightly packed I420 or NV12 frames with even dimensions.
Surface encoders accept GPU or camera input through their `ANativeWindow`.
Surface creation, surface EOS, and runtime bitrate or keyframe requests require API 26.
Encoded output exposes timestamps, flags, format changes, backpressure, and EOS.
The output format contains the configuration that the muxer needs.

`std/android/video/muxer` writes MP4 or WebM to a writable, seekable descriptor.
Tracks come from encoder output formats before muxer start.
A live output view passes directly to the muxer without a Fern packet copy.
The caller releases the codec buffer after that write.
Codec configuration buffers do not become media samples.
Container codec restrictions and sample order still apply.

`std/android/video/extractor` reads platform-supported containers from an explicit file range.
Its API 28 sample-size query checks limits before a write into a decoder input buffer.
`std/android/video/source` opens a granted content URI for this packet path.
It duplicates the asset descriptor and preserves the provider's offset and length.
An unknown length resolves from the seekable file's end. Non-seekable sources return `Unsupported`.
A caller-supplied 64-bit limit bounds the source range, separately from packet limits.
The adapter does not buffer the complete source or retain Java global references.
Close the extractor or player before source close. Each source has one owner.
The app needs a URI read grant, without broad storage permission.

The extractor preserves the complete native track format, including codec and color metadata.
It rejects encrypted and partial samples.

DRM and adaptive network streams remain unsupported.

`std/android/video/player` connects this extractor to a selected decoder and surface.
Each nonblocking `tick` submits at most one input and obtains at most one output.
It retains at most one pending output buffer.
The presentation clock converts media microseconds to monotonic nanoseconds with checked arithmetic.
Pause preserves the media position. Seek flushes buffers and restores configuration at a previous sync sample.

The player discards preroll frames before the target timestamp.

Runtime recovery closes the decoder and retries one software implementation policy.
It restarts from a sync sample near the last scheduled frame.
Hardware-only policy refuses software recovery.
Counters report scheduled and discarded frames, including final frames that also carry EOS.

The app owns the file descriptor and surface until player close.
This lower-level player controls one unencrypted video track.
Audio and A/V synchronization for this path remain future work.
The separate `std/android/player` path delegates complete A/V playback to Android.

Platform color metadata passes through the extractor and codec formats.
Missing identifiers remain unspecified. The app must select a suitable display surface and HDR mode.
This adapter does not implement tone mapping, color conversion, or HDR display qualification.


## IVF packets

`std/video/ivf` reads version-zero IVF headers for VP8, VP9, and AV1.
Packet results contain offsets, sizes, timestamps, and the next offset.
The reader does not copy packet payloads.
It can parse a complete packet prefix without the rest of the file.
The header frame count is advisory. Each packet has its own timestamp.

Timestamp conversion uses the declared rational timebase and checked arithmetic.

IVF support does not establish VP8, VP9, or AV1 software decode support.
AV1 temporal units can require further packet handling for a particular platform decoder.
Android MediaExtractor supplies its supported containers through the Android adapter.
Native Fern demuxers for MP4, MOV, Matroska, WebM, MPEG-TS, and fragmented streams remain future work.

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

The compiler differential suite includes the native video, codec-policy, and source-range tests.
The host ABI tests cover JNI discovery, candidate failures, software recovery, and resource cleanup.
They also cover encoder states, direct packet views, muxer order, extraction, pause, seek, and EOS.
The content-source mock checks real descriptor ranges, close-on-exec, JNI failures, and repeated open/close cycles.
These mocks do not decode video.

The Android workflow builds `tests/android/video/app.fern` and runs it on the API 34 emulator.
It requires AVC exports through both byte-buffer input and an EGL surface producer.
FFmpeg independently decodes all exported frames and checks their dimensions, count, and gray pixels.
The test requires surface playback, pause, resume, seek, and a gray screenshot.
The workflow also copies the byte-buffer export into MediaStore and grants the video APK access to that item.
The same codec-policy playback, pause, seek, frame-count, and screenshot checks apply to this content source.
Artifacts contain both MP4 files, codec names, Android build, elapsed times, and process memory.

Emulator results do not establish physical hardware throughput or battery consumption.

Run the same qualification on a connected API 29 or later phone.
The check reads the clips with `adb run-as`, so build the APK with `-apk-debuggable`:

```sh
bin/fern -apk build/video/fern-videotest.apk -apk-package dev.fern.videotest -apk-min-sdk 29 -apk-debuggable tests/android/video/app.fern
ANDROID_SERIAL=DEVICE python3 tests/android/video/emulator_check.py build/video/fern-videotest.apk build/video/device
```

Use `HardwareOnly` for device measurements that require hardware.
A hardware codec can reject a particular format or fail under resource pressure.
Record the selected codec attributes and the exact media request with each result.

The Android lane also runs `tests/android/player/app.fern` with an AVC and stereo AAC clip.
It checks both an HTTP source and the same clip in MediaStore through a granted content URI.
It requires video output, stable pause position, accurate seek, timeline anchors, completion, repeated playback, and error callbacks.

The error callback for a missing HTTP source comes after the retries of the platform, about 30 seconds after the request.
The fixture contains four seconds of 320 by 240 video and 48 kHz stereo audio.

The runner verifies that MediaStore returns the exact fixture bytes before playback.
It removes only the temporary MediaStore entry that it creates.
Artifacts include the fixture, independent FFprobe metadata, and separate HTTP and content playback diagnostics.
The test delegates A/V synchronization to Android. It does not measure acoustic lip-sync error or physical device power.

```sh
scripts/test-android-player
bin/fern -apk build/android-player/fern-playertest.apk -apk-package dev.fern.playertest -apk-min-sdk 26 -apk-permission INTERNET -apk-cleartext tests/android/player/app.fern
ANDROID_SERIAL=DEVICE python3 tests/android/player/emulator_check.py build/android-player/fern-playertest.apk build/android-player/device
```

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
The Android path now supplies platform demux, timestamps, sync-sample seek, and surface playback control.
The lower-level controller still needs audio synchronization and lifecycle integration.
The separate platform player supplies A/V synchronization. Both paths need representative device qualification.

Device tests must cover backpressure, resolution changes, EOS, flush, and playback after a seek.
HDR requires correct metadata, surface configuration, and display support.

Native software decoders for the common formats remain a separate implementation effort.
Their qualification requires official conformance vectors and independent decoded-plane comparisons.
Performance work requires representative clips, fixed compiler versions, and matched decoder settings.
CPU time, peak memory, allocations, frame latency, and device power require separate measurements.
Encoder comparisons require rate-distortion measurements on a shared corpus.
A decoder quality target means correct reconstruction and color metadata, rather than a tunable quality score.
