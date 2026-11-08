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
| Opus | Yes | Partial | SILK, hybrid, and CELT packets, multistream channel maps, loss concealment, and forward error correction. Ogg files decode at 48 kHz with gain and exact sample boundaries. The CELT encoder uses 48 kHz fullband mono or stereo. |
| Ogg | Pages | No | Incremental pages and packets, checksums, interleaved streams, and chained streams. The Opus layer supplies file playback. |
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
File-reader adapters remain separate work.

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
