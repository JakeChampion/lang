# Audio codecs

Status: [reference] — Initial implementation, 2026-11-07.

The codecs use Fern code on every target. They need no C library, process, or platform codec at runtime.
The Opus packet API reuses the Fern CELT implementation.

| Format | Decode | Encode | Current scope |
| --- | --- | --- | --- |
| WAVE | Yes | Integer PCM | Integer PCM at 8/16/24/32 bits and IEEE float at 32/64 bits, one through eight channels. Ordinary and extensible subformats. |
| AIFF/AIFC | Yes | No | Signed integer PCM at 1 through 32 bits, NONE/twos/sowt, IEEE float at 32 or 64 bits, and G.711 A-law/mu-law. Exact byte-view frame ranges. |
| AU | Yes | No | Big-endian signed integer PCM at 8/16/24/32 bits, IEEE float at 32 or 64 bits, and G.711 A-law/mu-law. Known and unknown data sizes. |
| FLAC | Yes | Fixed predictors | Native FLAC, 4 through 32 bits, one through eight channels. All predictor types, Rice/Rice2, wasted bits, and stereo assignments. |
| QOA | Yes | Yes | Static files, signed 16-bit PCM, one through eight channels. Independent frame APIs also accept individual frames. |
| ULC2 | Yes | Yes | Example library under `examples/ulc`, with block APIs, transient windows, and VBR/CBR/ABR encoding. |
| Opus | Yes | Partial | SILK, hybrid, and CELT packets, multistream channel maps, loss concealment, and forward error correction. Ogg files decode at 48 kHz with gain and exact sample boundaries. The CELT encoder uses 48 kHz fullband mono or stereo. |
| Ogg | Pages | No | Incremental pages and packets, checksums, interleaved streams, and chained streams. The Opus and Vorbis layers supply file playback. |
| MP3 | Yes | No | Indexed-bitrate MPEG-1/2/2.5 Layer III, mono/stereo, long/short/mixed blocks, bit reservoir, and recognized Xing/LAME gapless metadata. |
| AAC-LC | Yes | No | Raw access units and unprotected ADTS, 1024 samples per block, all 13 ADTS rates, and declared channel configurations 1 through 7. |
| MP4/M4A | AAC-LC | No | Self-contained audio tracks, sample tables, decoder configuration, track selection, and edit-list trimming. Complete byte views only. |
| Vorbis | Yes | No | Version one, one through eight channels, both floor types, all residue types, and standard block sizes. Incremental Ogg files and borrowed readers apply initial and final sample boundaries. |
| ALAC | Yes | No | Version zero, 16/20/24/32-bit integer PCM, one through eight channels, raw and compressed packets, and standard layouts. CAF supplies file playback. Compressed stereo requires at most 32 predictor bits. M4A remains open. |
| CAF | ALAC, PCM, float, G.711 | No | Version-one files, packet tables, bounded playback, exact sample seeks, and initial and final trim. Complete immutable byte views only. |
| HE-AAC, WavPack | No | No | No implementation in these modules. |

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

`examples/audio-convert.fern` converts AAC-LC ADTS, M4A, MP3, WAVE, native FLAC, or QOA input to WAVE, FLAC, or QOA output.
Integer input retains its bit depth. QOA output from integer input requires 16-bit samples.
AAC and MP3 input use float PCM. The example quantizes them to 32-bit WAVE or 16-bit QOA.

The example does not preserve container tags or custom channel layout metadata.
It rejects AAC configurations 4 and 7 because its output API cannot describe those speaker maps.
The convenience example uses the default allocation limits. It does not implement a complete player.

```sh
fern -target x86-64-linux -o audio-convert examples/audio-convert.fern
./audio-convert wav input.flac output.wav
./audio-convert wav input.mp3 output.wav
./audio-convert wav input.m4a output.wav
./audio-convert wav input.aac output.wav
./audio-convert qoa input16.wav output.qoa
./audio-convert flac input24.wav output.flac
```

## Native FLAC encoding

`flac.encode(samples, format, limits)` emits a complete lossless FLAC stream.
`std/audio/flac/encoder` also supplies block settings, independent frames, and STREAMINFO construction.
The encoder supports every sample depth from 4 through 32, one through eight channels, and sample rates from 1 through 1,048,575 Hz.
It compares stereo assignments and uses constant, verbatim, or fixed predictors with Rice/Rice2 residual partitions.
It writes frame checksums, the total sample count, and the PCM MD5.

The default block size is 4096 frames. The encoded byte budget is `max_input_bytes`.
`encode_frame` bounds each block and accepts a fixed frame number or a variable sample position.
For incremental output, the caller supplies STREAMINFO and maintains the stream count and PCM digest.
The complete-stream API requires at least one frame and computes that metadata itself.

The encoder performs no quantization. LPC analysis and matched performance qualification remain open in #14688 and #14515.
No compression, speed, or memory lead is claimed.

The frozen bank contains 81 bitstreams independently decoded by FFmpeg and libFLAC 1.5.0 with MD5 verification.
A separate exhaustive oracle checks 48 predictor, partition, Rice, escape-code, and wasted-bit cost minima.
Regenerate the banks with `scripts/gen-flac-encoder-vectors.py --encoder PATH` and `scripts/gen-flac-encoder-costs.py`.
The encoder executable comes from `scripts/audio-reference/flac-encode.fern`.

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

## AIFF, AIFF-C, and AU

`std/audio/aiff.inspect` and `std/audio/au.inspect` return an opaque `std/audio/linear.Index`.
The index retains scalar format and range metadata. It retains no encoded input or PCM.
The caller must preserve the input bytes after inspection.
Frame ranges permit exact seeks without earlier decode.
Multichannel samples retain their stored order.

`read_integer_into` consumes reusable `i32[]` storage and preserves its suffix after the valid sample prefix.
Integer samples retain their declared right-aligned depth. G.711 expands to signed 16-bit PCM.
Float input returns `Unsupported` through integer APIs.

`read_float_into` consumes reusable `f64[]` storage and preserves finite float values without clipping.
It normalizes integer samples to their signed full scale.
Non-finite float input returns `Sample`.

`information` supplies the sample format and frame count.
Each read checks the selected frame range, input extent, output capacity, and sample budget.
Inspection permits tracks longer than the output budget because each range has its own limit.
AIFF inspection also checks a chunk budget, sound offsets, and integral 80-bit sample rates.
Nonzero AIFF sound block sizes and other compression tags remain unsupported.

The 58 independent FFmpeg references cover every channel count from one through eight, byte order, partial sample bytes, and sample depths.
They include float values outside unity and every G.711 code word.
Tests compare all integer samples and normalized float bit patterns exactly.
Repeated frame ranges check suffix preservation and live buffer aliases.
The generator records the reference version and SHA-256 values for encoded bytes and both PCM representations.

```sh
python3 scripts/gen-linear-audio-vectors.py --check
```

The codecs perform no file I/O. Reader integration and additional container encoders remain open under #14687.

`std/audio/caf/linear` supplies CAF integer PCM, IEEE float, and G.711 playback through the same linear index.
Integer PCM uses 8, 16, 24, or 32 bits in either byte order.
Float PCM uses 32 or 64 bits in either byte order.
Constant packet tables can declare initial and final trim without an extra PCM copy.
The opaque index contains only the playable range.
ALAC playback retains its packet API in the parent CAF module.

The 36 additional CAF references retain every integer sample and normalized float bit pattern.
They cover all eight channel counts, both byte orders, float values outside unity, every G.711 code word, and unknown data size.
The trim case compares the playable slice of independent FFmpeg PCM.
Seven CAF boundary groups check descriptions, signed table fields, trim, empty presentations, limits, float errors, and every-byte mutations.

`std/audio/wav.inspect_linear` supplies the same range APIs for integer and IEEE float WAVE.
It accepts ordinary and extensible float subformats at 32 and 64 bits.
The existing integer header, decode, range, and encoder APIs retain their contracts.
`wav.decode_float` decodes complete files to normalized `f64` PCM without integer quantization.
The index also exposes the encoded data offset and byte count.

Thirty additional WAVE references compare every sample and float bit pattern exactly.
They cover all eight channel counts, integer depths, both float widths, and canonical extensible channel masks.
Six boundary groups cover float payloads, live aliases, GUIDs, masks, prefixes, range limits, empty files, and mutations.

## ALAC

`std/audio/alac.create` reads a 24-byte configuration or its 48-byte form with an explicit channel layout.
`information` declares sample depth, frame capacity, and the WAVE speaker mask.
Packets decode independently and need no earlier packet history.
State retains two channel scratch arrays, or one array for mono.
Low sample bits remain in the borrowed input during prediction.

`decode_packet_into` consumes a reusable integer output buffer and preserves its suffix after the valid samples.
`packet_frames` reads the first audio-element header and checks the declared output budget without residual decode.
`decode_packet` allocates exactly the required sample prefix. Full decode checks every channel, the end marker, and padding.
Integer PCM retains its declared right-aligned bit depth. It uses no float conversion or quantization.

The 38 independent cases cover all eight channel layouts, sample depths, special predictor modes, low bits, and raw packets.
Apple's pinned ALAC decoder supplies exact PCM. FFmpeg supplies a second reference for its supported cases.
The reference adapters explicitly reorder surround channels to the documented WAVE speaker order.
Source provenance and licenses are in `licenses/alac/NOTICE.txt`.

The reference build verifies pinned Git blob hashes before compilation:

```sh
python3 scripts/build-alac-reference.py --source-dir /tmp/apple-alac --output /tmp/alac-reference
python3 scripts/gen-alac-vectors.py --apple-reference /tmp/alac-reference --check
```

Compressed stereo with 33 predictor bits remains unsupported.
M4A playback and Reader integration remain open under [#14685](https://github.com/JakeChampion/lang/issues/14685).
Native, WASI, and Android compilation checks do not establish physical Android performance or power use.

`std/audio/caf` supplies CAF playback and exact byte-view seeks for ALAC.
Inspection retains packet offsets, sizes, positions, and configuration. It retains no encoded bytes or PCM.
The logical packet arrays use sixteen bytes per packet and an eight-byte terminal position, before capacity and runtime overhead.
Each cursor step decodes at most one packet. Seeks reuse decoder storage and need no earlier packet.

CAF audio events retain the original packet buffer and declare the playable range with `start` and `frames`.
Both values count frames. This avoids an extra PCM buffer for initial and final trim.
The channel mask and WAVE channel order follow supported explicit CAF layouts.
The complete-file convenience method checks a cumulative PCM budget.

The index checks packet headers and extents. Reads validate the selected audio payloads.
The playable end respects partial ALAC packets when a CAF packet table describes nominal full-packet capacity.
CAF readers, other CAF codecs, and custom channel descriptions remain open.

## Vorbis

`std/audio/vorbis.create` reads the identification and setup packets.
`std/audio/vorbis/headers` reads the separate comment packet and preserves its exact bytes.
The decoder checks version, framing, codebook completeness, setup references, and shared allocation budgets.
The single-entry codebook rule follows Vorbis Errata 20150226.

`Decoder.decode_packet` returns interleaved float PCM in WAVE speaker order.
The first packet primes overlap storage and returns zero frames.
Later packets produce one quarter of the sum of the previous and current block lengths.
Container code must use granule positions to trim initial and final PCM.

`std/audio/vorbisfile` decodes incremental Ogg files, including sequential chains and changes of rate or channel count.
It validates page checksums, sequence numbers, continuation flags, header placement, and granule progression.
The first nonfinal audio page can trim initial samples or establish a timestamp offset.
The final page can trim trailing samples. Positions count playable frames within each logical stream.

A sample event contains at most one packet of PCM. Complete-file PCM budgets belong to the caller.

`std/audio/vorbisfile/input` connects this decoder to a borrowed `Reader` or a byte-source callback.
Each cooperative step performs at most one source read and one decoder feed.
Pending events drain before another source read. Empty successful reads mean permanent EOF.
The caller retains and closes the reader. The adapter retains one bounded input chunk and codec state.

`std/audio/vorbisfile/seek` indexes an immutable byte view and returns exact PCM after a sample seek.
It retains packet locations and sample positions, with 32 logical bytes per packet before capacity and runtime overhead.
Seeks use binary search and one preceding packet for overlap. Seeks within a link reuse decoder configuration and scratch.

Each `step` performs at most one setup parse, one packet decode, or one link transition.
The index validates container structure, setup, mode headers, and sample boundaries. Reads validate the spectral payload.

Input bytes must remain unchanged after inspection. The default index permits 16 MiB and 1,048,576 packets across selected links.
Reader-backed seek indexes and performance qualification remain open under [#14519](https://github.com/JakeChampion/lang/issues/14519).

`decode_packet_into` consumes a reusable PCM buffer. Entries after the valid prefix retain their values.
`packet_frames` checks the mode and computes the required output frame count without decode.
Packet errors consume the decoder. `reset` clears overlap history and retains configuration and scratch storage.

Legal early residue endings preserve complete values and leave the remaining values zero.
An incomplete floor becomes unused. Packet type and mode fields must be complete.

The decoder supports blocks from 64 through 8,192 samples.
State retains at most 4,096 overlap entries per channel, plus bounded spectral, floor, and transform scratch.
Codebooks and Bark maps are prepared once. Packet syntax and classification arrays still allocate.
The API does not promise an allocation-free audio callback.

Independent float PCM fixtures use the public synthesis API from Xiph libvorbis 1.3.7.
They cover rate and channel changes between streams, quality settings, transients, and synthetic type-zero floors.
Complete-file fixtures use libvorbisfile's seekable public float API and include split headers, continued comments, both trim boundaries, timestamp offsets, and chains.
Separate transform fixtures use the defining cosine equation and whole-timeline overlap accumulation in binary64 C.
The native implementation derives from pinned Symphonia source under MPL-2.0.
See [the notice](../licenses/vorbis/NOTICE.txt) for attribution, reference scope, and license terms.

## AAC-LC

`std/audio/aac.decode` decodes a complete ADTS byte view to interleaved `f32` samples.
It checks cumulative input and output limits. A format change returns `Unsupported` in this whole-file API.

`std/audio/aac/config.inspect` reads an MPEG-4 AudioSpecificConfig.
Create a decoder with `aac.create`, then call `Decoder.decode_packet` for each raw access unit.
The packet API is suitable for a container demuxer that supplies complete access units.
`std/audio/mp4` supplies complete-view MP4/M4A demux and playback.

`Decoder.decode_adts` reads one complete ADTS frame at a byte offset.
It supports one through four raw data blocks and both MPEG identifiers.
A configuration change resets overlap state before decode.
Both packet methods have an `_into` form that consumes a reusable PCM buffer.
The valid sample prefix has `frames * channels` entries. The remaining buffer entries retain their values.

Decoder state retains channel overlap, window shapes, element tags, and a noise seed.
The transform scratch uses 4,224 float entries. Channel overlap uses 1,024 entries per channel.
Channel syntax and spectral arrays still allocate during decode. The API does not promise an allocation-free audio callback.

Output uses WAVE speaker order for the declared AAC configuration.
Configuration 7 places its extra front pair after the back pair.
Some encoders use configuration 7 for a side pair instead. ADTS cannot distinguish these interpretations.
An application must retain layout knowledge from its source when it needs an exact speaker map.

The decoder supports long and grouped short windows, sine and Kaiser-Bessel windows, all spectral codebooks, escapes, and pulse data.
It also supports mid-side stereo, intensity stereo, noise substitution, and temporal noise shaping.
Packet errors consume the decoder. The caller can create a new one from its retained configuration.
`Decoder.reset` clears overlap and noise state while retaining transform storage.

HE-AAC/SBR/PS, coupling-channel elements, program configurations, and 960-sample frames return `Unsupported`.
Protected ADTS frame decode also returns `Unsupported` until CRC verification is implemented.
Header inspection still exposes protection and raw-block counts.
These remaining AAC tasks stay open under [#14517](https://github.com/JakeChampion/lang/issues/14517).

The AAC implementation derives from pinned Symphonia source under MPL-2.0.
See [the notice](../licenses/aac/NOTICE.txt) for attribution, source revision, and license scope.

## MP4 and M4A

`std/audio/mp4.inspect` indexes self-contained AAC tracks from a complete byte view.
The index retains configuration, packet ranges, packet times, and edit lists. It retains no encoded file bytes.
Use the same immutable file bytes when calling `decode_track` with a retained index.
Track numbers select entries in the audio-track array. Track identifiers preserve the container identifiers.

`mp4.decode` selects the first enabled audio track. `mp4.decode_track` selects an explicit audio track.
Both call the native AAC-LC packet decoder. The packet API also lets callers supply their own encoded-frame reader.
Packet offsets use signed 64-bit counts. The complete-view API still needs the encoded file in memory during inspection and decode.

The parser supports ordinary and extended box lengths, parent-to-end boxes, and metadata before or after media data.
It reads fixed and per-sample `stsz` sizes, all three `stz2` widths, `stco`, `co64`, `stsc`, and `stts`.
Each packet must fit within a declared `mdat` payload.
Sample descriptions must identify self-contained `mp4a` data and a bounded MPEG-4 decoder configuration.
QuickTime version-one sample headers and a `wave` wrapper for `esds` are also accepted.

Movie and media clocks retain their distinct units.
Normal-rate edit lists remove encoder delay and final padding. Empty edits produce silence, and repeated edits reuse the requested media range.
Cumulative clock conversion prevents a rounding error at each edit from changing the total track length.
Decode allocates the final presentation buffer and one reusable AAC frame buffer.
It does not allocate a second full-track PCM buffer before trimming.

Limits bound input bytes, boxes, tracks, packet records, sample descriptions, edits, and output samples.
The packet-record budget applies across all audio tracks.
Defaults permit 65,536 boxes, 32 tracks, 1,048,576 packet records, 64 descriptions per track, and 64 edits per track.
The audio limits also bound each decoded AAC frame and the complete presentation output.

Fragmented media, encryption, external data references, other audio codecs, format-changing descriptions, and nonzero composition offsets return `Unsupported`.
Reader-backed metadata indexing and cooperative file playback remain separate work.
These limits keep [#14518](https://github.com/JakeChampion/lang/issues/14518) open after this complete-view playback milestone.

Fifteen independent fixtures compare complete trimmed PCM with FFmpeg at an absolute tolerance of 0.000001.
They cover fast-start files, multiple audio tracks, a video track, compact sizes, extended boxes, and 64-bit chunk offsets.
The reference generator uses the independently reported container duration to exclude final codec padding from FFmpeg output.
Boundary tests cover incomplete prefixes, malformed tables, media ranges, budgets, track selection, and edit-clock rounding.
They also check 64-bit packet ranges beyond eight GiB without allocating a large file.

## MP3

`std/audio/mp3` provides native Layer III decode with normalized float output.
Samples can exceed the device range. Apply gain or `pcm.quantize` at the conversion boundary.
The decoder supports indexed bitrates in MPEG-1, MPEG-2, and MPEG-2.5 streams.
It rejects Layer I, Layer II, and free-format bitrate headers.

`decode_raw` retains all coded samples and codec delay.
`decode` skips a Xing or Info metadata frame and applies recognized LAME delay and padding.
`inspect_track` exposes those boundaries. It skips ID3v2.2 through ID3v2.4, ID3v1, and bounded trailing APE tags.
The module does not expose tag contents. `std/audio/mp3/seek` supplies a separate seek index.

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

## Sequential MP3 input

`std/audio/mp3/input` accepts a borrowed `Reader` or a bounded source callback.
It decodes a track without retaining all encoded bytes or decoded samples.
The caller closes the reader after success or failure.

`step` performs at most one source read or one frame decode.
`next` continues until `Begin`, `Samples`, `End`, or completion.
Sample events identify their playable position after recognized Xing/LAME delay and padding.

Input limits bound the encoded window and individual frames.
The metadata budget covers skipped ID3 payloads and trailing tags.
The reader retains at most 192 metadata bytes for trailing ID3v1 and APE validation.
An APE payload that starts with complete MP3 frames needs a Xing frame count on non-seekable input.

Empty source reads mean permanent EOF. I/O errors retain their own error category.
Checksums, declared counts, and trailing metadata can fail after earlier PCM events.
`End` confirms complete validation. Completion performs no further source reads.

The tests use all 16 reference tracks with three input chunk sizes.
They also cover large metadata, one-byte reads, gapless output, limits, and late I/O failures.
The independent interpreter smoke uses the pinned scalar minimp3 PCM fixture.
Native and WASI file tests verify caller ownership of the open reader.

## Sequential FLAC input

`std/audio/flac/input` accepts a borrowed `Reader` or bounded source callback.
It emits positioned integer PCM without retaining the complete track.
The default encoded window is 4 MiB. Source reads use at most 16 KiB.
PCM and encoded frame limits apply to each frame. Metadata has a separate cumulative budget.

Each `step` performs at most one source read or frame decode attempt.
Short reads increase the decode threshold geometrically.
The reader checks fixed and variable frame sequences, sample counts, and frame checksums.
It computes the STREAMINFO MD5 incrementally when the stream supplies one.

`End` confirms complete validation at physical EOF.
A late MD5 mismatch or source error can follow earlier PCM events.
The caller retains and closes the source on success or failure.
Unknown sample counts and large metadata do not require complete-track storage.

## Indexed MP3 and FLAC playback

`std/audio/mp3/seek` and `std/audio/flac/seek` build bounded frame indexes.
Each cursor returns at most one codec frame of PCM per `next` call.
The output declares its playable position and frame count.
The caller supplies the same immutable encoded input view to each call.
These APIs do not perform file I/O.

MP3 index construction scans frame headers without decoding PCM.
The cursor applies recognized Xing/LAME delay and padding once.
A seek restores up to 511 bytes of reservoir history before two complete frames warm the overlap and synthesis filters.
FLAC index construction decodes frames to verify checksums, lengths, and sequence, without retaining their PCM.
FLAC seeks use a binary search of sample positions and need no codec history.

The logical MP3 index stores four bytes per audio frame.
The logical FLAC index stores twelve bytes per frame.
Array capacity, headers, and allocator overhead add to those counts.
Input and index limits bound separate storage. The sample limit applies to each decoded frame.
FLAC index construction needs an output budget for the STREAMINFO maximum block size.

Tests compare repeated forward and backward seeks with slices of complete reference-qualified decode.
They cover frame interiors, boundaries, the exact track end, MP3 gapless metadata, and fixed and variable FLAC blocks.
Every compiled sample matches exactly.
The independent MP3 interpreter smoke uses a float slice from hash-pinned scalar minimp3.
Regenerate it with `scripts/gen-mp3-seek-smoke.py`.

## Indexed MP3 files

`std/audio/mp3/file` builds a validated index from a bounded seekable source.
It retains a logical 16-byte entry for each audio frame, plus array and allocator overhead.
Each entry supplies a 64-bit file offset, an encoded byte count, and a reservoir payload count.
Index construction decodes one frame at a time without retaining complete input or PCM.

Tail inspection uses at most 160 final bytes and an optional 32-byte APE header.
It excludes trailing tags before audio decode, even when their payload begins with complete MP3 frames.
The metadata limit covers leading and trailing tags together.

Playback restores reservoir history and warms two complete frames before output.
Recognized Xing/LAME delay and padding apply once.
Each cooperative step performs at most one seek, bounded source read, or frame decode.
The caller supplies the same immutable file and preserves its position during pending reads.

## Indexed FLAC files

`std/audio/flac/file` builds an index through a bounded seekable source.
Index construction decodes each frame and verifies the complete-stream MD5 when supplied.
It retains offsets and sample positions, without retaining encoded input or PCM.
The logical index uses 24 bytes per frame. Capacity and allocator overhead add to this count.

Playback seeks directly to one indexed frame and reads only its encoded range.
File offsets and track positions use 64-bit counts.
Each cooperative step performs at most one seek, source read, or frame decode.
Index construction also has a cooperative step API.

The caller owns the reader and supplies the same immutable file for all calls.
Do not reposition it during a pending window read.
An end-of-track seek completes without source operations.
The shared `std/audio/file` helper reads bounded windows, including ranges beyond 32-bit offsets.

## Ogg

`std/audio/ogg` reads bounded pages and packets from partial input.
Each `Decoder.feed` call returns the next decoder, the consumed byte count, and an optional completed page.
Call it again with the unconsumed bytes. Call `finish` after the final input.
An error requires a new decoder.

The reader checks capture bytes, version, flags, checksums, stream sequences, and continuation flags.
It tracks byte offsets and signed granule positions.
It supports packets across pages, interleaved logical streams, and sequential chains.
Its limits bound active streams, individual packets, and aggregate incomplete packet storage.
The page buffer holds at most 65,307 bytes. A page returns at most 255 completed packets.

The reference corpus uses unmodified libogg revision `be05b13e98b048f0b5a0f5fa8ce514d56db5f822`.
It contains 56 pages and 48 packets, with 471,404 encoded bytes across five cases.
The tests compare page fields, every packet checksum, packet lengths, and first-byte and first-page offsets.
Each case runs with complete input and partial input. Small pages also run one byte at a time.
Separate tests cover malformed headers, truncation, sequence errors, continuation errors, integer overflow, and storage limits.

Regenerate the corpus and the checksum table with these commands:

```sh
python3 scripts/gen-ogg-vectors.py
python3 scripts/gen-ogg-tables.py
```

The reference generator needs cc and CMake. It verifies the pinned archive hash before the build.
Normal tests need no libogg library or network access.

`std/audio/opusfile/headers` reads Opus identification packets and bounded comment packets.
It accepts channel mapping families 0, 1, and 255, with explicit stream and channel counts.
Comments retain raw bytes and binary suffix data. Field lookup ignores ASCII case.
The reference corpus checks 514 identification headers and 2,295 comment packets against pinned libopusfile.
Private channel mappings and the 255-channel bound have separate RFC checks.

`std/opus/packet.parse_delimited` reads one stream from a multistream packet.
It returns a normal Opus packet and the consumed byte count.
Its 1,896 reference cases compare boundaries, frame lengths, padding, and packet bytes with pinned libopus.

```sh
python3 scripts/gen-opus-headers-vectors.py
python3 scripts/gen-opus-delimited-vectors.py
```

## Ogg Opus files

`std/audio/opusfile` decodes complete files from incremental input.
It emits identification, comment, sample, and end events.
It selects the first Opus logical stream, then accepts subsequent chains, including changes in channel count.
Other interleaved streams remain subject to Ogg validation and storage limits.

Call `feed` with the unconsumed input until it is consumed.
Events can consume zero bytes. After the last input, drain events with `feed([])` until no event remains, then call `finish`.
An error requires a new decoder.

Output uses interleaved float samples at 48 kHz and the declared channel map.
The file decoder applies header gain, pre-skip, initial granule offsets, and final sample trimming.
The input sample rate remains metadata. Output can exceed the device sample range.
Each sample event reports its playable position and absolute granule timestamp.
Each end event reports the playable frame count for that logical stream.

The decoder retains one page of completed packets, bounded partial packets, and codec state.
A packet can span pages. Each sample event contains at most one packet of decoded output.
The default limits allow eight coded streams, 46,080 raw output samples per packet, and 1 MiB per encoded packet.
Separate limits bound comments, active Ogg streams, partial packet storage, and the number of logical links.
Output limits include samples that pre-skip or final trimming removes.

The complete-file corpus compares 139,814 float samples across ten files and twelve logical links with pinned libopusfile.
It covers mono through eight-channel surround, positive and negative gain, large comment packets, continued audio packets, and changing chain formats.
It also covers initial granule offsets, several packets per page, a single audio page, and a track with zero playable frames.
Each case runs with complete and partial input. Sample differences must not exceed 0.000001.

Separate tests check malformed pages, truncation, limits, pending output, and signed 64-bit timing boundaries.
An independent complete file checks the self-host interpreter.

Regenerate the file corpus with `scripts/gen-opus-file-vectors.py`.
The generator verifies pinned libopusfile, libopus, and libogg archive hashes.
Normal tests need no reference library or network access.
The bounded byte-source adapter supplies sequential file reads.

### Bounded Opus byte sources

`std/audio/opusfile/input` connects the incremental file decoder to a byte source.
It retains one input chunk, bounded container state, and codec state.
It does not retain the complete encoded track or collect PCM output.
The chunk size accepts one byte through 1 MiB.

`create(file_limits, chunk_size)` creates the input state.
`step(reader)` borrows a Fern `Reader` and returns the next state and one read event.
`step_with(callback)` supports custom byte sources with the same bounded read contract.
A callback returns at most the requested bytes. An empty successful read means permanent end of input.

I/O errors remain distinct from codec errors through `ReadIo` and `ReadAudio`.

Each step performs at most one source read and one decoder feed.
Pending output drains before another source read, including audio that a completed page queues without an immediate event.
`Pending` gives control to the application between steps. `Output` contains a file event.
`Finished` confirms complete validation at physical end of input.
Subsequent calls return the end without another source read.

`next(reader)` and `next_with(callback)` continue through pending steps until an event or the end.
Source reads can block. A source needs appropriate deadlines when the application requires bounded latency.
The caller retains and closes the reader, including after a decode error.
An application cancels further work when it discards the input state. This releases its buffers.

The adapter supplies sequential reads. Indexed file seeks remain separate work.

Tests compare the complete independent file corpus through short source reads.
They check read counts, backpressure, EOF validation, source-size violations, and late I/O errors.
The interpreter smoke suite compares independent PCM through one-byte reads and the `next_with` API.
Real-file tests check both APIs, exact PCM, reader ownership, truncated files, and missing files on native and WASI targets.

### Indexed Opus seeks

`inspect_seek` validates the complete immutable encoded input and builds an index without PCM decode.
The index retains packet locations, raw positions, durations, and link metadata.
Its parallel arrays require 24 logical bytes per packet, plus array capacity.
The default limits allow 16 MiB of encoded input and 1,048,576 packets.
The file, codec, comment, link, and per-packet output limits also apply before decode.

`seek_reader(index)` consumes the index and creates a cursor.
`seek(link, position)` selects a playable frame and resets codec state.
It uses at least 80 ms of preroll when that history exists.
The preroll limit accepts 80 ms through ten seconds at 48 kHz.
Finite preroll can produce different PCM from a decode that starts at the beginning.
This is also true for libopusfile.

`seek_exact(link, position)` replays the full history of the selected link.
Its PCM matches a complete decode, with no retained PCM or decoder checkpoints.
Its decode cost increases with the target position.
Both methods accept the end position and continue into subsequent links on the next read.
Output preserves gain, pre-skip, final sample boundaries, absolute timestamps, and channel maps.

`step(input)` decodes at most one packet or advances one link.
It returns `SeekPending`, `SeekSamples`, or `SeekEnd` with the next cursor.
This API lets an application cancel or apply backpressure between packet decodes.
`next(input)` continues until samples or the end.
The input view must contain the same unchanged bytes that created the index.
Each sample result contains at most one packet of PCM.

The seek corpus compares 90 cold seeks and 760,044 float samples with independent libopusfile output.
Separate exact seeks compare full-history PCM at the same 0.000001 tolerance.
Tests cover repeated forward and backward seeks, chains, gain, trimming, continued packets, zero output, and invalid limits or positions.
Packet extraction also matches the sequential Ogg decoder.
The interpreter smoke corpus retains independent PCM checks for both seek policies.

Regenerate the seek corpus with `scripts/gen-opus-seek-vectors.py`.
The generator verifies all three reference archive hashes and checks the complete-file fixtures before it freezes seek results.

## Opus and Android

`std/opus` provides stateful SILK, hybrid, and CELT packet decode, plus CELT packet encode.
The packet API handles multi-frame packets and padding.
The decoder accepts all five Opus output rates and mono or stereo output.
`decode_lost` conceals missing packets. `decode_fec` recovers a preceding packet when the next packet contains redundancy.
Decode that next packet normally after the FEC call.

`std/opus/multistream` combines coupled and mono streams with explicit channel maps.
It supports repeated channels, silent channels, and decoded channels that the output omits.
Its limits bound input bytes, stream count, and output samples.

The `decode_into`, `decode_lost_into`, and `decode_fec_into` methods consume reusable output buffers.
Each result declares the valid frame count. Entries after the valid samples retain their values.
Codec scratch space still allocates within bounded limits.

The multistream corpus compares 972,000 float samples across 605 steps with pinned libopus.
It covers all five output rates, mono through eight-channel surround, custom maps, loss, FEC, and reset.
Every final entropy range matches. Sample differences must not exceed 0.000001.
An independent six-step subset also checks the self-host interpreter.
Regenerate both corpora with `scripts/gen-opus-multistream-vectors.py`.

The whole-frame CELT encoder retains its libopus 1.4 reference at `82ac57d9f1aaf575800cf17373348e45b7ce6c0d`.
Its transform tables, rotation angle, and stereo-energy epsilon stay separate from the current decoder reference.
An unmodified scalar C helper reproduces the six existing encoder-frame and band-analysis trace checksums.
The default MDCT and VQ kernel tests continue to check the current reference.
The additional encoder table data contains 7,240 logical bytes, before array and allocator overhead.

Regenerate the encoder tables and trace fixtures with these commands:

```sh
python3 scripts/gen-opus-encoder-modes.py
python3 scripts/gen-opus-encoder-vectors.py
```

`std/opus/silk/math` supplies fixed-point arithmetic. `std/opus/silk/predict` reconstructs stable prediction filters.
`std/opus/silk/entropy` decodes frame parameters. `std/opus/silk/core` reconstructs integer PCM for one coded channel.

The entropy and predictor tables come from pinned Pion Opus sources.
`std/opus/silk` decodes internal-rate packets, including stereo and redundant frames.
`std/opus/silk/resample` converts one channel to each Opus output rate and retains the reference input delay.
`std/opus/silk/output` combines internal-rate packets, persistent resamplers, and output channel conversion.
`silk.decode_lost` and `output.decode_lost` retain fixed-point concealment and comfort noise state across missing packets.
The public Opus facade combines these modules with CELT and retains state through codec mode transitions.

SILK reads implicit zero bits at the end of the entropy stream, as the reference decoder requires.
Payload truncation is not always detectable. The container or transport must supply the encoded frame boundaries.

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
python3 scripts/gen-silk-output-vectors.py --source-dir /path/to/pion-opus
python3 scripts/gen-silk-output-vectors.py --source-dir /path/to/pion-opus --loss
# Requires cc, CMake, and the pinned archive or network access.
python3 scripts/gen-silk-loss-vectors.py
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

The complete output path matches PCM checksums, boundary samples, and final range for 150 received libopus packets.
These are prefixes before packet loss from 50 review-corpus cases, with all output rates and mono/stereo channels.
The complete playback corpus separately checks packet loss recovery and mode transitions.

The loss path matches PCM checksums, boundary samples, and entropy range for all 600 steps of those 50 cases.
The cases include loss bursts, recovery, comfort noise, output-rate conversion, and internal-rate changes.
A separate 54-step corpus uses an unmodified C decoder with an observation-only resampler wrapper.
It covers startup loss, 10/20 ms duration changes, stereo changes, and mediumband excitation history.
The generator verifies the pinned libopus archive before it builds the reference.

The public decoder corpus compares 212,220 float samples across 290 steps with pinned libopus.
It checks all output rates, both channel counts, codec modes, transitions, loss, FEC, and final entropy ranges.
The maximum permitted absolute sample difference is 0.000001.
Separate kernel traces match complete CELT, MDCT, vector quantization, and band results bit for bit.
Regenerate these corpora with `scripts/gen-opus-playback-vectors.py` and `scripts/gen-opus-kernel-vectors.py`.

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

# AAC reference packets and complete decoded PCM use FFmpeg.
python3 scripts/gen-aac-vectors.py --check
# AAC transforms use an independent C double-precision equation.
python3 scripts/gen-aac-kernel-vectors.py --check
python3 scripts/gen-aac-tables.py --check
python3 scripts/gen-mp4-vectors.py --check

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
The standard compiler differential suite includes the audio, Opus, MP3, and AAC test programs.

The AAC tests compare every decoded sample with frozen FFmpeg PCM at an absolute tolerance of 0.000001.
The 44 fixtures cover all 13 ADTS rates, seven channel configurations, silence, transients, intensity stereo, and noise substitution.
Directly written syntax fixtures cover all 11 spectral codebooks, escape values through 8,191, pulse data, and eight TNS combinations.
An independent C double-precision oracle checks both inverse-transform sizes and all window sequences and shapes.
The Huffman tests check all 1,362 pinned raw codewords and their incomplete byte prefixes.
Boundary tests check raw packets, multi-block ADTS, reset, buffer reuse, truncated input, invalid syntax, and resource limits.

A separate test checks the correlated-PNS same-vector rule from ISO/IEC 14496-3 section 4.6.13.3.
FDK AAC v2.0.3 independently confirms equal, nonzero channels for that directly written packet.
Its source pin is `716f4394641d53f0d79c9ddac3fa93b03a49f278`.
The helper is `scripts/audio-reference/aac-pns.cpp`. It links FDK AAC only as a reference tool.

PNS permits different random generators. This check compares the channel relationship, not random sample phase.

FFmpeg 7.1.5 generates independent channels for that correlated packet.
Its PCM is therefore unsuitable as the oracle for this particular same-vector test.

These tests establish the measured AAC-LC scope. They do not establish complete AAC conformance or performance leadership.

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

The codec stack now includes complete Opus decode, Ogg demux, and MP3/FLAC seek indexes.
Further MP3 work includes free-format streams, damaged-stream synchronization, and more tag formats.
AAC work includes HE-AAC/SBR/PS, protected ADTS verification, and program configurations.
MP4/M4A work includes fragmented media, further codecs, and bounded file-reader playback.
Vorbis, further encoders, and other lossless formats need independent reference vectors and benchmarks.
Android qualification still needs physical-device memory, power, thermal, and sustained-playback measurements.

Additional formats need their own reference vectors and benchmarks before the support matrix changes.
No metadata inspector or unsupported-format placeholder counts as a codec implementation.
