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

## Native AAC file input

`std/audio/mp4/file` indexes AAC tracks through a borrowed seekable source.
It shares the video file scanner and the existing audio configuration, table, clock, and edit readers.
The scanner reads bounded movie metadata and top-level headers. It skips encoded media payloads.
The index retains scalar AAC configurations and absolute packet ranges, without encoded audio or PCM.
`std/audio/file.Window` can then read one bounded packet from the same source.

File length and packet offsets use signed 64-bit values.
Separate limits bound movie bytes, source windows, operations, boxes, tracks, descriptions, edits, and packets.
The nested encoded-input limit bounds both movie bytes and each encoded packet.
Short reads are supported. Each seek and read consumes an operation.

The caller owns the immutable source and closes it. Skipped physical payload bytes require later packet-read validation.

This API supplies input for incremental A/V work. It does not provide a streaming decoder or change the Android controller.
The caller must maintain AAC decoder state and apply the existing presentation plan for priming, edits, and padding.
The complete-view AAC and ALAC decoders keep their existing APIs.
The lower-level controller remains video-only, with external AAudio clock anchors.
The separate platform `MediaPlayer` path still supplies platform A/V playback.

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

The separate `std/android/audio.Stream.timestamp` query exposes the AAudio frame position and its monotonic time in nanoseconds.
It returns `None` before the platform supplies a valid anchor, including the delay after an asynchronous start.
The existing stream rate supplies the frame units. The query does not change playback state or assign media timestamps.

`std/android/video/player.synchronize_audio` can anchor video deadlines to this existing playback stream.
The app supplies a matching audio frame and media-time origin. The conversion reuses the checked track-time rescaler.
The conversion rounds down to whole microseconds without an intermediate multiplication overflow.

An unavailable timestamp preserves the previous video clock. A rejected anchor preserves the player and its pending output buffer.
The existing video timeline remains active until a valid audio anchor replaces it.
The method requires an active, unpaused video controller and performs no audio state or I/O operation.

The app supplies PCM and coordinates pause, resume, and seek across both streams.
A video seek or codec recovery clears the clock, so the app must supply a matching new audio anchor.
The controller remains video-only, without audio decode, integrated A/V transport control, or physical lip-sync qualification.

## Native MP4 video packets

`std/video/mp4` indexes self-contained AVC, HEVC, VP9, AV1, and QTRLE video tracks without Android or an external codec library.
It reuses the existing ISO box, sample-table, clock, and data-reference readers.
It preserves packet ranges, descriptions, decode order, decode times, composition times, durations, and sync-sample flags.
Each packet range refers to the original byte view, without a payload copy.

Native `av01` descriptions require one `av1C` box.
`std/video/av1/configuration` checks its record fields and each self-delimited configuration OBU without a payload copy.
A sequence header is optional. If present, it must come first and match the record fields and sample-entry maximum dimensions.
The index retains the complete record in `csd0`, empty `csd1`, and `length_size` zero.

This layout also matches Android's AV1 configuration convention. Native indexing does not call Android.

Other configuration OBU payloads remain opaque. The parser does not validate coded samples, sample groups, HDR metadata, decoder-model delays, or complete profile/level conformance.
AV1 packet ranges preserve the existing clocks, offsets, descriptions, and source budgets.
Both complete-view and seekable-file indexes use this path. The native AV1 pixel-decode column remains unchanged.

Absent composition and sync tables use packet defaults without per-sample temporary arrays.
Explicit tables retain their validation, including the distinction between absent and empty sync tables.
Explicit sync tables use validated sample numbers through a scalar cursor, without a temporary boolean for every packet.

Explicit composition tables retain validated source runs, without a temporary 64-bit offset for every packet.
The packet loop advances at each run boundary and preserves unsigned version-zero and signed version-one offsets.
Complete run coverage passes validation before any offset enters a packet.
When every sample has a run, positive counts and complete coverage prove that each count is one.
The public timing readers still return one value per sample.

AVC and HEVC descriptions use the existing NAL configuration readers.
The index validates configuration framing and dimensions. It does not decode pixels or validate coded parameter-set syntax.
The native decode and encode columns above remain unchanged.

With more than 16 media ranges, the shared sample-table reader validates their order and uses a binary search for each packet.
Both complete-view and seekable-file indexers supply this order, without a new range array.
Chunk offsets can move backward between media ranges.
Direct sample-table callers retain the previous behavior for unordered or overlapping ranges through a linear search.

QuickTime Animation MOV descriptions support 24-bit RGB and 32-bit ARGB packets through the existing native QTRLE codec.
The description supplies packed depth, dimensions, and an empty NAL configuration.
AVC and HEVC descriptions report zero packed depth, without a component-precision claim.
The same bounded complete-view and seekable-file indexers preserve QTRLE packet ranges, times, and sync flags.
Decode must start at a sync packet and retain previous pixels for delta packets.
Palette depths, RGB555, nonzero sample-description versions, and multiple frames per sample remain unsupported.

Legacy MOV files without `ftyp` are accepted only when every indexed video description is QTRLE.
The AVC and HEVC file-type requirements remain unchanged.

`std/video/avc` reads an ordinary H.264 SPS NAL without a platform codec.
It reports coded and visible dimensions, crop margins, chroma format, bit depth, and VUI metadata.
The reader checks emulation prevention, bounded scaling lists, picture-order syntax, HRD entries, and trailing bits.
The byte budget applies before the unescaped copy. The pixel budget applies to the complete coded picture before crop.

This is native sequence-header inspection, without picture reconstruction or complete stream conformance checks.
It does not validate pictures, level constraints, or decoder support.

`std/video/hevc` reads one base-layer H.265 SPS NAL without a platform codec.
It reports profile identifiers, coded and visible dimensions, crop margins, precision, sublayer bounds, and VUI metadata.
The byte budget and an 8 KiB SPS ceiling apply before the unescaped copy.
The pixel budget covers the complete coded picture before crop.

The reader checks ordinary SPS syntax and the nine range-extension flags through exact trailing bits.
Fixed bounds constrain scaling lists, reference sets, long-term references, sublayers, and HRD entries.
Other extensions and nonzero profile spaces return `Unsupported`.
Profile constraint bits remain opaque. The reader does not resolve VPS references or validate pictures, level constraints, or decoder support.
The native pixel-decode table remains unchanged.

`std/video/hevc/pps` inspects one base-layer PPS with its matching complete SPS NAL.
It reports prediction, quantizer, tile, filter, scaling, and range-extension metadata through exact trailing bits.
The reader reuses the SPS syntax and checks its sequence identifier, coding-block geometry, and precision bounds.
It validates explicit tile extents without a retained map or coefficient array.

Each NAL has its own packet budget. The existing 8 KiB SPS ceiling and a 64 KiB PPS ceiling apply before unescape.
The pixel budget covers the SPS coded picture before crop.
Other PPS extensions return `Unsupported`. This is parameter metadata, without picture reconstruction or full profile and level conformance.

`std/video/avc` also inspects a PPS with its matching parsed sequence.
It reports entropy and prediction flags, reference defaults, quantizers, slice-group metadata, and optional scaling flags.
The reader validates all seven slice-group map types without a retained map allocation.
The caller's packet budget and a 64 KiB PPS ceiling apply before the unescaped copy.
The coded-picture budget bounds map syntax, including interlaced map units.
This is parameter metadata, without scaling coefficients, map contents, or reconstructed pixels.

The NAL conversion, MP4 indexer, and Android selection APIs retain their existing contracts.

Times use the track clock's ticks. Version-one composition offsets can produce negative presentation times.
The index preserves edit lists in their original movie-duration and media-time units.
An application must apply these edits to construct the movie presentation timeline.
Fragmented media, encryption, and external data references return `Unsupported`.

The input must be a complete byte view, within an explicit input limit.
Separate budgets bound boxes, tracks, descriptions, edits, packets, dimensions, and each packet's size.
The packet budget is cumulative across video tracks. The track budget includes audio and other non-video tracks.
The complete byte-view API does not stream a large file or define an exact process-memory bound.

`std/video/mp4/file` indexes the same tracks from a borrowed seekable source.
It reads top-level headers and one movie metadata box. It skips media payloads without a copy.
It reuses the same nested box and sample-table readers.
The source length and packet offsets use signed 64-bit counts.

Its defaults permit files up to 64 GiB and movie metadata up to 16 MiB.
Separate limits bound each input window and the number of seek and read operations.
Short reads are permitted. Empty reads before a window completes report truncation.
I/O errors retain their own category.

The returned index retains metadata and packet ranges, without encoded payloads.
These limits do not define an exact process-memory bound.

The caller retains and closes the source after success or failure.
The source must remain immutable through index and packet reads.
Use `std/audio/file.Window` to read one indexed packet range through the same source.
Skipped payloads still need truncation checks when the caller reads them.
These synchronous calls must run outside a latency-sensitive Android loop callback.

## Android packet path

`std/android/video` configures an NDK MediaCodec decoder for an existing `ANativeWindow`.
It requires API level 21 or later.
Decoded frames remain in the platform surface, without a Fern pixel copy.
The API exposes input backpressure, output format changes, EOS, flush, and timed output release.
Output format changes report dimensions, crop, and available color identifiers.

Each format change must satisfy the pixel limit from decoder open.
The limit applies to complete output dimensions, before crop.
An oversized format returns `Limit`. The lower-level player enters its failed state before it schedules more output.
This check does not bound memory that MediaCodec allocates before its format event.

Absent color identifiers remain zero. The adapter does not invent a color profile.

`std/video/nal` converts MP4 length prefixes to Annex B start codes.
It also reads NAL ranges from a complete Annex B byte view and converts them to length prefixes.
The iterator borrows its input and excludes start codes and surrounding zero bytes.
It preserves emulation-prevention bytes inside each unit.
The converter checks complete input and output budgets, plus the capacity of each length prefix.

`std/video/nal/input` reads Annex B units through a bounded sequential byte source.
Each step performs at most one read and returns at most one owned unit with a 64-bit relative payload offset.
Limits cover the input window, unit payload bytes, unit count, and read operations.
The scanner preserves its position across short reads and reuses the complete-view framing parser for each finished unit.

An empty source returns `End`. Incomplete prefixes or missing payloads return `Truncated`, and I/O errors retain their category.
This reader does not identify access units or provide timestamps. A decoder still needs complete access units and timing from the caller.
The caller owns the source and must run blocking I/O outside the Android loop callback.

Both functions inspect packet framing without a platform codec.
They do not identify access units or supply timestamps. A muxer still needs packet boundaries and timing from the caller.

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

`Source.read_at` reads relative asset offsets through `pread` without a descriptor-position change.
Each request permits at most 1 MiB before allocation or I/O.
The method clamps reads to the asset range and returns short reads or an empty array at EOF.
Native AAC and video packet readers can use this method through their existing callback APIs.
These synchronous reads belong outside frame and audio callbacks.

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

`replace_surface` changes the output window without a seek, codec restart, or pending-buffer release.
It preserves pause and the presentation clock. Later codec recovery uses the last successful window.
The app owns both compatible windows and keeps them valid through the call.
After success, the app can release its old window reference.

A null window preserves the player and returns `Header` before the platform call.
A platform error marks the player as failed. The app must close the player or recover its codec.
This method does not detach a surface. Close the player before window destruction if no replacement exists.

Runtime recovery closes the decoder and retries one software implementation policy.
It restarts from a sync sample near the recovery point.

After a seek, recovery uses the requested media time until a new frame is scheduled.
This prevents recovery from restoring the frame position that preceded the seek.
Pause preserves this recovery point. Discarded frames do not advance it.

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

`std/video/ivf/input` reads these packets from a borrowed sequential source.
Each step performs at most one bounded read and emits at most one event.
It accepts short reads and distinguishes I/O errors from malformed or truncated input.
It validates the declared packet length before a payload read.
Separate limits bound input-buffer bytes, packet bytes, packet count, and source-read operations.

Packet events own their encoded bytes and preserve unsigned 64-bit timestamps without an order requirement.
Payload offsets use signed 64-bit counts from the source start.
EOF between packets returns `End`. EOF inside a header or payload returns `Truncated`.
The header frame count remains advisory.
The caller retains and closes the source after success or failure.

The input-buffer limit excludes retained packet events and does not define an exact process-memory bound.
Blocking source reads must run outside the Android loop callback.

IVF support does not establish VP8, VP9, or AV1 software decode support.
`std/video/vp9.inspect_keyframe` reads coded and render dimensions, precision, color fields, and tile-header metadata from one VP9 keyframe.
It validates the complete uncompressed syntax through zero alignment and bounds the declared compressed-header range.
The packet budget applies before inspection. The pixel budget covers both coded and render dimensions.

The reader retains no input bytes and uses fixed syntax loops within a 128-byte header window.
It does not parse entropy-coded headers or tiles, establish complete frame boundaries, or decode pixels.
Inter frames and show-existing commands require reference state and return `Unsupported`.
The caller supplies a single frame without an IVF header or superframe index.

`std/video/vp9.split_packet` extracts byte ranges for up to eight indexed frames from one packet.
It checks the packet budget before index access and validates every size before it returns ranges.
The bounded index occupies at most 34 bytes. No payload bytes are copied or retained.
A packet without an index returns one opaque range, without a complete-frame claim.

Superframe markers require complete, matching indices with positive sizes within the payload.
The result reports any unindexed bytes before the index separately, without a padding-validity claim.
Frame payloads, reference state, and separate frame timestamps remain the caller's responsibility.
The same ranges can select individual keyframes for header inspection without a container or platform codec.

AV1 temporal units can require further packet handling for a particular platform decoder.
`std/video/av1.next_obu` reads self-delimited low-overhead OBU ranges without a payload copy.
It checks header bits, extension identifiers, eight-byte size fields, and unsigned payload bounds within the packet budget.
An absent size field or reserved OBU type returns `Unsupported`.
Annex B framing, payload syntax, temporal-unit ordering, complete frame boundaries, and pixel decode remain outside this API.

`std/video/av1/sequence.inspect` reads one complete sequence-header OBU through the same framing reader.
It reports maximum frame dimensions, precision, color fields, and sequence tool flags without a payload copy.
The pixel budget applies to the maximum frame area. A separate 512-byte payload ceiling bounds header work.
Fixed loops cover up to 32 operating points, timing fields, and trailing bits.

These maximum dimensions do not supply each frame's coded or render dimensions.
Operating-point relationships, profile/level conformance, reference state, and reconstructed pictures remain outside this metadata API.
The native AV1 decode column remains unchanged.

Android MediaExtractor supplies its supported containers through the Android adapter.
The native MP4 indexer above supports self-contained AVC, HEVC, VP9, AV1, and QTRLE tracks.
Other MOV codecs, Matroska, WebM, MPEG-TS, and fragmented streams remain future work.

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

`scripts/check-video-av1-interop` compares native and real WASI OBU ranges with the FFmpeg coded-bitstream reader.
Six fresh streams cover 8-, 10-, and 12-bit output across 4:2:0, 4:2:2, and 4:4:4 chroma.
Both libaom and dav1d must decode identical planes before and after Fern identifies the padding for removal.
These external decoders supply the pixel comparison. Fern supplies packet framing only.

`scripts/check-video-av1-sequence-interop` compares 22 native and real WASI metadata values with FFmpeg CBS.
Six encoder headers cover 8-, 10-, and 12-bit precision. Fifteen synthetic headers cover timing, decoder models, 32 operating points, and color branches.
The real streams also match FFprobe metadata and every decoded plane through libaom and dav1d.
Synthetic sequence-only carriers establish header syntax, without coded pictures or a pixel-decode claim.

`scripts/check-video-hevc-interop` compares 36 native and real WASI metadata fields with independent FFmpeg CBS results.
Eight libx265 streams cover 8-, 10-, and 12-bit precision, monochrome, chroma formats, crop, color, and aspect ratios.
FFprobe metadata and every decoded sample from all three lossless frames must agree with the original raw input.
Fifteen synthetic SPS headers cover seven sublayers, scaling lists, predicted reference sets, PCM, and HRD bounds.
Synthetic headers establish syntax only. Four malformed controls require explicit independent parser diagnostics.

`scripts/check-video-hevc-pps-interop` compares 44 native and real WASI metadata fields with FFmpeg CBS.
Eight lossless streams preserve all three source frames. Thirty-two synthetic parameter pairs cover tiles, quantizers, scaling, and range extensions.
Synthetic pairs contain no coded pictures. Four malformed controls require explicit independent field and parse-failure diagnostics.

Twenty-four FFmpeg packets cover RGB24, ARGB32, alpha, keyframes, partial rows, and unchanged frames.

The widths cross literal, repeat, and skip boundaries.
Fern decodes each packet to the expected ARGB CRC.
FFmpeg also decodes twenty-four Fern packets with exact equality to the original ARGB bytes.
AVC and HEVC configuration and packet conversions preserve FFmpeg decoded pixels in two additional streams.

`scripts/check-video-annex-b-interop` creates five fresh AVC and HEVC streams, including 10-bit profiles.
Native and WASI ranges match an independent parser. Converted streams retain identical decoded 8-bit or 10-bit YUV planes in FFmpeg.

`scripts/check-video-qtrle-mov-interop` compares five fresh MOV files with FFprobe and FFmpeg.
Native and real WASI input preserve every packet field and decoded ARGB sample.
Fixtures cover RGB24, ARGB32, delta and unchanged frames, fast-start metadata, two video tracks, interleaved AAC, and legacy file headers.
Normal tests use checked-in references and verify all sync-sample starts and bounded file reads without media payload access during indexing.

`scripts/check-video-avc-interop` compares 30 native and WASI SPS fields with independent FFmpeg readers.
Eight fresh streams cover profile, precision, chroma, crop, interlace, color metadata, and HRD cases.
Six synthetic headers cover scaling lists, separate color planes, 255 picture-order offsets, and two maximum HRD tables.
FFmpeg's coded bitstream reader independently accepts those synthetic headers.
Normal native and WASI tests use checked-in references and malformed syntax controls, without an FFmpeg dependency.
The malformed-input cases cover packet truncation, runs, skips, dimensions, output budgets, and integer overflow.

`scripts/test-video` executes the native suite with the sanitizer and the WASI suite.
It also builds ARM64 Android shared libraries.
On Linux, it executes the MediaCodec ABI test through a shared library for the host architecture.
The unit workflow runs this script on x86-64 and ARM64 hosts.

The compiler differential suite includes the native video, MP4 packet and file-input, codec-policy, and source-range tests.
The host ABI tests cover JNI discovery, candidate failures, software recovery, and resource cleanup.
They also cover encoder states, direct packet views, muxer order, extraction, pause, seek, and EOS.
The content-source mock checks real descriptor ranges, close-on-exec, JNI failures, and repeated open/close cycles.

`scripts/check-android-content-reads` also checks short reads, a full 1 MiB payload, truncation, and unchanged descriptor positions.
Native AAC and AVC packet checks cover asset offsets above 8 GiB.
The host controls combine normal and sanitized Fern libraries with normal and UBSan C mocks.
These mocks do not decode video.

The Android workflow builds `tests/android/video/app.fern` and runs it on the API 34 emulator.
It requires AVC exports through both byte-buffer input and an EGL surface producer.
FFmpeg independently decodes all exported frames and checks their dimensions, count, and gray pixels.
The test requires surface playback, pause, resume, seek, and a gray screenshot.

It also requires the real codec to accept its same output surface while paused.
Distinct-window replacement and pending-buffer behavior use host ABI controls. The emulator check does not qualify window recreation on physical devices.

The workflow also copies the byte-buffer export into MediaStore and grants the video APK video read permission.
The same codec-policy playback, pause, seek, frame-count, and screenshot checks apply to this content source.

Before playback, the app reads the content asset in 4096-byte chunks through the native source method.
The fixture limits the asset to 2 MiB and computes incremental SHA-256 without a complete encoded copy.
The harness compares its byte count and digest with an independent Android content read.
Portable native-sanitizer and WASI tests check this checksum helper and its refusal paths.
The WASI tests do not execute Android descriptor reads.
Artifacts contain both MP4 files, codec names, Android build, elapsed times, and process memory.

The APK build lane also runs `scripts/check-video-mp4-interop` on fresh FFmpeg fixtures.
It compares native packet ranges, timestamps, durations, and sync flags with FFprobe.
It compares complete-view and seekable-file packet indexes for all six generated fixtures.
Native and WASI file readers must return the same packet bytes and metadata.
It extracts AVC and HEVC Annex B streams through bounded file windows.
It compares every FFmpeg-decoded RGB sample with the original MP4 decode.

An additional sparse AVC file places media beyond 8 GiB and checks packet offsets against FFprobe.
The sparse file occupies less than 1 MiB of disk blocks and does not enter repository artifacts.

Native and WASI file-input tests reject every media-payload read during index creation.
They cover short reads, an 8 GiB sparse range, truncated boxes, resource limits, and I/O failures.
Fixtures cover reordered frames, signed composition offsets, fast-start layout, interleaved AAC, multiple video tracks, compact sample sizes, and 64-bit offsets.
Normal native and WASI tests use checked-in reference bytes and require no FFmpeg installation.

The same build lane runs `scripts/check-video-ivf-interop` on fresh VP8, VP9, and AV1 files.
Native and WASI readers must return identical packet bytes through three-byte source reads.
FFprobe independently checks packet sizes, timestamps, and offsets.
FFmpeg decodes the reconstructed IVF files and compares every RGB sample with the original decode.
Native and WASI boundary tests cover partial input, invalid lengths, exact budgets, I/O failures, and source offsets beyond 4 GiB.

Emulator results do not establish physical hardware throughput or battery consumption.

The audio test APK also records whether AAudio supplies a timestamp before stream stop.
An unavailable anchor remains an explicit diagnostic. It does not qualify timestamp availability or acoustic lip-sync accuracy.

Run the same qualification on a connected API 29 or later phone.
The check reads the clips with `adb run-as`, so build the APK with `-apk-debuggable`:

```sh
bin/fern -apk build/video/fern-videotest.apk -apk-package dev.fern.videotest -apk-min-sdk 29 -apk-permission READ_MEDIA_VIDEO,READ_EXTERNAL_STORAGE -apk-debuggable tests/android/video/app.fern
ANDROID_SERIAL=DEVICE python3 tests/android/video/emulator_check.py build/video/fern-videotest.apk build/video/device
```

Use `HardwareOnly` for device measurements that require hardware.
A hardware codec can reject a particular format or fail under resource pressure.
Record the selected codec attributes and the exact media request with each result.

The Android lane also runs `tests/android/player/app.fern` with an AVC and stereo AAC clip.
It checks both an HTTP source and the same clip through a MediaStore content URI.
It requires video output, stable pause position, accurate seek, timeline anchors, completion, repeated playback, and error callbacks.

The error callback for a missing HTTP source comes after the retries of the platform, about 30 seconds after the request.
The fixture contains four seconds of 320 by 240 video and 48 kHz stereo audio.

The runner verifies that MediaStore returns the exact fixture bytes before playback.
It removes only the temporary MediaStore entry that it creates.
Artifacts include the fixture, independent FFprobe metadata, and separate HTTP and content playback diagnostics.
The test delegates A/V synchronization to Android. It does not measure acoustic lip-sync error or physical device power.

The test APKs declare `READ_MEDIA_VIDEO` and `READ_EXTERNAL_STORAGE` for the shell-owned MediaStore fixtures.
The runners grant `READ_MEDIA_VIDEO` on API 33 and later, or `READ_EXTERNAL_STORAGE` on earlier releases.
The shell cannot grant these fixtures through an activity intent.
These tests use broad video read permission. They do not verify document-picker grants for individual items.
The player API still requires the caller to hold read access when it opens a content URI.

```sh
scripts/test-android-player
bin/fern -apk build/android-player/fern-playertest.apk -apk-package dev.fern.playertest -apk-min-sdk 26 -apk-permission INTERNET,READ_MEDIA_VIDEO,READ_EXTERNAL_STORAGE -apk-cleartext tests/android/player/app.fern
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

## Matched QTRLE qualification

`scripts/compare-video-qtrle` compares the public Fern and FFmpeg QTRLE decode APIs on identical packets.
FFmpeg supplies a separate native reference executable for the qualification tool.
The Fern decoder and the Android app do not acquire an FFmpeg dependency.

The tool generates four-frame RGB24 and ARGB32 sequences with repeated runs, literal pixels, sparse changes, and dense changes.
It includes one unchanged frame per sequence. Full workloads use 512 by 288 and 1920 by 1080 images.
Both decoders must reproduce every canonical ARGB byte before timing starts.
A separate native sanitizer build and optional WASI execution must reproduce those bytes too.

Each process preloads its packets and retains only the current frame.
FFmpeg uses one decoder thread. Each process completes one corpus cycle before the timed loop.
The loop measures public decode calls and one sampled pixel per frame with `CLOCK_MONOTONIC`.
Setup, frame verification, output writes, and process startup remain outside that interval.

Pairs alternate their execution order. The report preserves every trial, checksums, compiler and binary hashes, tool versions, and corpus hashes.

A small native sampler forks each measured process after its own exec.
This excludes the Python parent's inherited RSS floor.
The sampler records peak process RSS and complete-process user and system CPU time through Linux `wait4`.
RSS includes runtime code, shared libraries, preloaded packets, and frame storage. It is not a decoder-only allocation budget.

Fern pixel arrays use a different representation from FFmpeg's packed pixel buffers.
A separate instrumented executable counts Fern allocations. Its time and RSS do not enter the comparison.
Assembly checks require the timed executable to omit allocation instrumentation.
FFmpeg allocation counts remain unmeasured.

The tool requires Linux, a C compiler, FFmpeg, its development headers, and `pkg-config`.
WASI verification also requires `wasmtime`.

```sh
scripts/compare-video-qtrle --wasi --output build/video/qtrle-comparison.json
scripts/compare-video-qtrle --smoke --wasi --output build/video/qtrle-smoke.json
```

`--smoke` verifies the sampler, malformed-input rejection, complete frames, and timed checksums on small images.
It records no performance ratios. The Android build lane runs this check with WASI verification and retains its report.

The workloads repeat four packets on one host. They do not represent every animation, general video decode, or full application playback.
Shared-host timings and process RSS require interpretation. They do not establish world-leading performance, physical-device power, or lip-sync accuracy.

The [2026-11-08 x86-64 checkpoint](benchmarks/video-qtrle-2026-11-08/README.md) contains the first matched results.
That compiler takes 2.2 to 7.7 times the FFmpeg loop time on those four workloads.

The [2026-11-09 checkpoint](benchmarks/video-qtrle-2026-11-09/README.md) repeats the same packets with compiler source `15a62e417f`.
Fern takes 2.3 to 7.4 times the FFmpeg loop time in that run.
These results identify allocation and memory targets. They do not establish a general decoder ranking.

The [run-state allocation control](benchmarks/video-qtrle-run-state-2026-11-09/README.md) compares both decoder versions with one frozen compiler.
Scalar run validation reduces heap allocations by 83% to 94% across the four workloads.
Every decoded pixel remains identical. Median time differences stay within the shared-host noise threshold, and peak RSS remains unchanged.
The result establishes fewer allocations without a claim of general speed or peak-memory improvement.

## Player work and qualification order

The common playback priorities are H.264, HEVC, VP9, and AV1, followed by legacy formats.
The Android path now supplies platform demux, timestamps, sync-sample seek, and surface playback control.
The lower-level controller accepts an app-owned AAudio clock anchor but still needs integrated audio decode, transport control, and lifecycle integration.

It accepts both `audio.Stream` and `audio.Music` anchors. Stereo positions count frames, rather than individual channel samples.
The separate platform player supplies A/V synchronization. Both paths need representative device qualification.

Device tests must cover backpressure, resolution changes, EOS, flush, and playback after a seek.
HDR requires correct metadata, surface configuration, and display support.

Native software decoders for the common formats remain a separate implementation effort.
Their qualification requires official conformance vectors and independent decoded-plane comparisons.
Performance work requires representative clips, fixed compiler versions, and matched decoder settings.
CPU time, peak memory, allocations, frame latency, and device power require separate measurements.
Encoder comparisons require rate-distortion measurements on a shared corpus.
A decoder quality target means correct reconstruction and color metadata, rather than a tunable quality score.

## Native AV1 MP4 qualification

`scripts/check-video-mp4-av1-interop` creates fresh AV1 MP4 files with FFmpeg and compares native sanitizer and real-WASI results.
It checks every packet field and byte against FFprobe and the original file.
Both index paths must agree. Extracted packets retain every decoded plane through independent libaom and dav1d decoders.
Synthetic configuration references establish header syntax only. FFmpeg remains test-only.

The configuration format follows AOM AV1 ISOBMFF version 1.3.0.
The record can omit a sequence header. The API then reports no sequence maximum and does not read media to obtain it.
The existing sequence parser still checks a present header through its trailing bits and bounded zero padding.
These checks do not establish complete ISO BMFF or AV1 stream conformance.

## Native VP9 MP4 qualification

`std/video/mp4` and its seekable source path accept native `vp09` descriptions through the same bounded tables and clocks.
One version-one `vpcC` box is required. Flags and initialization-data length must be zero.
`std/video/vp9/configuration.inspect` checks the complete twelve-byte payload before it reports eight scalar fields.
The packet budget applies before access. The result retains no input bytes.

Profile, precision, chroma, level identifiers, and RGB constraints follow the [VP Codec ISO Media File Format Binding version 1.0](https://www.webmproject.org/vp9/mp4/).
Color identifiers remain scalar values. Sample headers, record-to-sample agreement, color conformance, and complete profile/level conformance remain outside this reader.
The seekable index does not read media payloads to infer these properties.

Configuration uses WebM CodecPrivate in `csd0`, zero `length_size`, and empty `csd1`.
The profile, level, precision, and chroma fields use identifiers one through four and one-byte lengths.
This follows Android's `MakeVP9CodecPrivateFromVpcC` conversion in `media/module/metadatautils/MetaDataUtils.cpp`.
The index retains its existing limit on container color metadata. It reports zero packed depth for VP9.

`scripts/check-video-mp4-vp9-interop` creates eight fresh, three-frame lossless VP9 MP4 files.
FFmpeg CBS checks keyframe profile, precision, chroma, and range. FFprobe checks known color identifiers and packet metadata.
The muxer's complete record supplies level and chroma-location identifiers that the VP9 keyframe does not contain.

Both Fern index paths preserve every packet field and byte through native sanitizer and real WASI executables.
Independent FFmpeg and libvpx decoders compare every plane with the original raw input before and after extraction.
FFmpeg remains test-only. Fern does not decode VP9 pixels.

## Native AAC and lower-level Android video integration

The video test app also coordinates native AAC PCM with an app-owned AAudio music stream.
The lower-level controller still owns one platform-decoded video track. The separate MediaPlayer A/V path remains unchanged.
This fixture does not add a general A/V player API or a native AVC pixel decoder.

The fixture contains twelve AVC frames and 19,200 stereo AAC frames at 48 kHz.
The existing native MP4 audio decoder applies encoder-delay and final-presentation trims.
The app quantizes its float output to interleaved signed 16-bit PCM.
Input bytes have a two-MiB limit. Decoded output has a 48,000-sample limit.
Reads use four-KiB chunks, and each nonblocking AAudio write requests at most 1,024 stereo frames.

Partial writes preserve the remaining samples. Pause stops new audio writes and video submissions.
Resume restarts both transports and permits a fresh AAudio clock anchor.
Seek creates a new audio stream and selects PCM from the requested frame, so its frame-zero origin has explicit media units.
The app rejects an unexpected stream rate instead of applying an incorrect frame-to-time scale.
Completion and window destruction close the app-owned audio stream.

The existing emulator runner adds this round after its video-only export checks.
It verifies both submitted sample counts, the pause and seek diagnostics, and two available-or-unavailable timestamp observations.
Unavailable timestamps remain distinct from valid anchors. They preserve the controller's existing timeline.
A short grace after queue completion does not measure acoustic output or establish physical drain accuracy.

`scripts/check-android-native-av` checks native sanitizer and real-WASI PCM against an independent FFmpeg decode.
Every quantized sample must differ by at most one signed 16-bit sample unit.
Host AAudio controls cover partial writes, backpressure, pause/resume, seek origins, rate rejection, startup failure, and exact cleanup counts.
Normal and sanitizer Fern libraries run against both normal and undefined-behavior-sanitizer C controls.
ARM64 shared compilation remains separate from execution. Representative device power, latency, and lip-sync qualification remain unmeasured.
