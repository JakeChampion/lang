# MPEG Layer III audio

This module decodes indexed-bitrate MPEG-1, MPEG-2, and MPEG-2.5 Layer III audio.
It supports mono, stereo, joint stereo, short blocks, mixed blocks, and the bit reservoir.
It verifies a supplied frame CRC. It does not support Layer I, Layer II, or free-format bitrate headers.

Output samples are interleaved, normalized `f32` values. Peaks can exceed -1 or 1.
Use `pcm.quantize` for integer PCM conversion at a device or file boundary.

`decoder` creates bounded state. `decode_frame_into` consumes that state and a reusable output buffer.
The buffer needs at most 2304 entries. Only the first `frames * channels` entries contain output.
A result with `needs_history` consumes input and retains reservoir data but supplies zero valid frames.

This permits decoder warmup after a seek. A decode error resets decoder state.

Each frame reports its sample rate, channel count, and consumed byte count.
A sample-rate or MPEG-version change resets the transform state. Channel-mode changes retain state.
The frame API requires a complete frame at a known byte offset. It does not scan damaged input for synchronization.

`decode_raw` retains all coded samples and codec delay. `decode` skips a Xing or Info frame and applies recognized LAME delay and padding.
`inspect_track` exposes those boundaries. Both whole-file calls require a constant output format.
The decoder skips ID3v2.2 through ID3v2.4, ID3v1, and bounded trailing APE tags.
It does not expose tag contents or build a seek index.

`inspect_track` reports padding after decoder-delay adjustment, with a minimum of zero.

`decode` validates the frame chain and raw sample budget before output allocation.
`decode_raw` checks cumulative limits as it decodes each frame.
The raw budget includes codec delay and padding. This bounds work even when metadata declares a short result.

The frame APIs retain at most 511 reservoir bytes, 576 overlap values, and 960 synthesis values.
They still allocate bounded scratch arrays. They do not promise zero allocations.

The implementation derives from the scalar [minimp3 reference](https://github.com/lieff/minimp3/tree/ea99364f61c14656440e8d77e9c233ccf3124633).
The minimp3 authors dedicated that source to the public domain under [CC0](https://creativecommons.org/publicdomain/zero/1.0/).
The module executes Fern code and needs no external codec library.

```fern
<<*>>=
// Native MPEG Layer III decode, derived from minimp3.
// https://github.com/lieff/minimp3/tree/ea99364f61c14656440e8d77e9c233ccf3124633
// CC0: https://creativecommons.org/publicdomain/zero/1.0/
import "std/audio/pcm";
import "std/audio/wire";
import "std/audio/mp3/header";
import "std/audio/mp3/bits";
import "std/audio/mp3/spectral";
import "std/audio/mp3/synthesis";
import "std/f32";
import "std/f64";

pub const DECODER_DELAY: i32 = 529;

pub struct Decoder { reservoir: u8[], overlap: f32[], qmf: f32[], sample_rate: i32, channels: i32, version: i32 }

pub struct Frame { sample_rate: i32, channels: i32, frames: i32, bytes_read: i32, needs_history: boolean, samples: f32[] }

pub struct Decoded { sample_rate: i32, channels: i32, samples: f32[] }

pub function decoder(): Decoder {
  return Decoder { reservoir: [], overlap: spectral.zeros(576), qmf: spectral.zeros(960), sample_rate: 0, channels: 0, version: 0 };
}

pub function (own d: Decoder) reset(): Decoder {
  return decoder();
}

pub function inspect(input: [u8], at: i32, limits: pcm.Limits): Result[header.Header, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  return header.parse(input, at);
}

function crc16(input: [u8], own crc: i32): i32 {
  for b in input {
    crc ^= b as i32 << 8;
    for _ in 0..8 {
      crc = (crc << 1 ^ (if ((crc & 32768) != 0) { 32773 } else { 0 })) & 65535;
    }
  }
  return crc;
}

function keep_reservoir(data: [u8], pos: i32): u8[] {
  let start = (pos + 7) / 8;
  if (data.len() - start > 511) {
    start = data.len() - 511;
  }
  let output: u8[] = [];
  for i in start..data.len() {
    output = output.append(data[i]);
  }
  return output;
}

// Frame decode keeps reservoir, granule, and synthesis state in stream order.
// fern-lint: allow cyclomatic-complexity
function decode_valid(own d: Decoder, input: [u8], at: i32, own output: f32[], limits: pcm.Limits): Result[(Decoder, Frame), pcm.AudioError] {
  let h = inspect(input, at, limits)?;
  let count = pcm.sample_count(h.frames, h.channels, limits)?;
  if (input.len() - at < h.bytes) {
    return Err(pcm.Truncated);
  }
  if (output.len() < count) {
    return Err(pcm.Buffer);
  }
  if (d.overlap.len() != 576 || d.qmf.len() != 960 || d.reservoir.len() > 511) {
    return Err(pcm.Header);
  }
  if (d.sample_rate != 0 && (d.sample_rate != h.sample_rate || d.version != h.version)) {
    d = decoder();
  }
  let side_start = at + 4 + (if (h.crc) { 2 } else { 0 });
  let main_start = side_start + header.side_bytes(h);
  if (main_start > at + h.bytes) {
    return Err(pcm.Header);
  }
  if (h.crc) {
    let crc = crc16(input[side_start:main_start], crc16(input[at + 2:at + 4], 65535));
    if (crc != wire.be(input, at + 4, 2) as i32) {
      return Err(pcm.Checksum);
    }
  }
  let side = spectral.side_info(input[side_start:main_start], h)?;
  let Decoder { reservoir, overlap, qmf, sample_rate: _, channels: _, version: _ } = d;
  let main: u8[] = [];
  let start = if (reservoir.len() > side.begin) { reservoir.len() - side.begin } else { 0 };
  for i in start..reservoir.len() {
    main = main.append(reservoir[i]);
  }
  for i in main_start..at + h.bytes {
    main = main.append(input[i]);
  }
  if (reservoir.len() < side.begin) {
    let state = Decoder { reservoir: keep_reservoir(main, 0), overlap: overlap, qmf: qmf, sample_rate: h.sample_rate, channels: h.channels, version: h.version };
    return Ok((state, Frame { sample_rate: h.sample_rate, channels: h.channels, frames: 0, bytes_read: h.bytes, needs_history: true, samples: output }));
  }
  let part_sum = 0;
  for g in side.granules {
    part_sum += g.part;
  }
  if (part_sum > main.len() * 8) {
    return Err(pcm.Header);
  }
  let b = bits.new(main.len() * 8);
  let pos0 = pcm.zeros(40);
  let pos1 = pcm.zeros(40);
  for granule in 0..if (h.version == 1) { 2 } else { 1 } {
    let samples = spectral.zeros(1152);
    for ch in 0..h.channels {
      let g = side.granules[granule * h.channels + ch];
      let end = b.pos + g.part;
      let s = if (ch == 0) { spectral.scales(main, b, g, h, pos0, ch)? } else { spectral.scales(main, b, g, h, pos1, ch)? };
      let spectral.Scales { bits: scale_bits, scale, positions } = s;
      if (ch == 0) {
        pos0 = positions;
      } else {
        pos1 = positions;
      }
      if (scale_bits.pos > end) {
        return Err(pcm.Header);
      }
      let (next, values) = spectral.huffman(main, scale_bits, g, scale, end, samples, ch * 576)?;
      b = next;
      samples = values;
    }
    if (h.channels == 2) {
      let (values, positions) = spectral.stereo(samples, pos1, side.granules[granule * 2], side.granules[granule * 2 + 1], h);
      samples = values;
      pos1 = positions;
    }
    for ch in 0..h.channels {
      let g = side.granules[granule * h.channels + ch];
      let longs = (if (g.mixed) { 2 } else { 0 }) << (if (h.rate_index == 2) { 1 } else { 0 });
      let aa = if (g.short_bands != 0) { longs - 1 } else { 31 };
      if (g.short_bands != 0) {
        samples = spectral.reorder(samples, ch * 576 + longs * 18, g, g.long_bands);
      }
      let (values, ov) = synthesis.hybrid(samples, overlap, ch * 576, ch * 288, g.block, longs, aa);
      samples = values;
      overlap = ov;
    }
    let (next, values) = synthesis.granule(qmf, samples, h.channels, output, granule * 576 * h.channels);
    qmf = next;
    output = values;
  }
  for i in 0..count {
    if (!output[i].is_finite()) {
      return Err(pcm.Sample);
    }
  }
  let state = Decoder { reservoir: keep_reservoir(main, b.pos), overlap: overlap, qmf: qmf, sample_rate: h.sample_rate, channels: h.channels, version: h.version };
  return Ok((state, Frame { sample_rate: h.sample_rate, channels: h.channels, frames: h.frames, bytes_read: h.bytes, needs_history: false, samples: output }));
}

pub function (own d: Decoder) decode_frame_into(input: [u8], at: i32, own output: f32[], limits: pcm.Limits): (Decoder, Result[Frame, pcm.AudioError]) {
  return match (decode_valid(d, input, at, output, limits)) {
    Ok((next, frame)) => (next, Ok(frame)),
    Err(e) => (decoder(), Err(e)),
  };
}

pub function (own d: Decoder) decode_frame(input: [u8], at: i32, limits: pcm.Limits): (Decoder, Result[Frame, pcm.AudioError]) {
  let h = match (inspect(input, at, limits)) { Ok(v) => v, Err(e) => { return (decoder(), Err(e)); } };
  let count = match (pcm.sample_count(h.frames, h.channels, limits)) { Ok(v) => v, Err(e) => { return (decoder(), Err(e)); } };
  return d.decode_frame_into(input, at, spectral.zeros(count), limits);
}

function id3_footer(input: [u8], at: i32, size: i32): Result[void, pcm.AudioError] {
  let f = at + 10 + size;
  if (!wire.tag(input, f, "3DI")) {
    return Err(pcm.Header);
  }
  for i in 3..10 {
    if (input[at + i] != input[f + i]) {
      return Err(pcm.Header);
    }
  }
  return Ok(());
}

function id3_size(input: [u8], at: i32): Result[(i32, boolean), pcm.AudioError] {
  let version = input[at + 3] as i32;
  let flags = input[at + 5] as i32;
  if (version < 2 || version > 4 || input[at + 4] == 255) {
    return Err(pcm.Unsupported);
  }
  let reserved = if (version == 2) { 63 } else if (version == 3) { 31 } else { 15 };
  if ((flags & reserved) != 0) {
    return Err(pcm.Header);
  }
  let size = 0;
  for i in 6..10 {
    if (input[at + i] > 127) {
      return Err(pcm.Header);
    }
    size = size << 7 | input[at + i] as i32;
  }
  return Ok((size, version == 4 && (flags & 16) != 0));
}

pub function audio_start(input: [u8], limits: pcm.Limits): Result[i32, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  let at = 0;
  while (wire.tag(input, at, "ID3")) {
    if (input.len() - at < 10) {
      return Err(pcm.Truncated);
    }
    let (size, footer) = id3_size(input, at)?;
    let total = 10 + size + (if (footer) { 10 } else { 0 });
    if (total > input.len() - at) {
      return Err(pcm.Truncated);
    }
    if (footer) {
      id3_footer(input, at, size)?;
    }
    at += total;
  }
  return Ok(at);
}

// Raw stream decode checks format and cumulative limits at each frame boundary.
// fern-lint: allow cyclomatic-complexity
pub function decode_raw(input: [u8], limits: pcm.Limits): Result[Decoded, pcm.AudioError] {
  let at = audio_start(input, limits)?;
  let end = audio_end(input)?;
  if (at > end) {
    return Err(pcm.Header);
  }
  let d = decoder();
  let samples: f32[] = [];
  let rate = 0;
  let cc = 0;
  while (at < end) {
    let h = inspect(input, at, limits)?;
    if (h.bytes > end - at) {
      return Err(pcm.Truncated);
    }
    if (rate != 0 && (rate != h.sample_rate || cc != h.channels)) {
      return Err(pcm.Unsupported);
    }
    let count = pcm.sample_count(h.frames, h.channels, limits)?;
    if (samples.len() > limits.max_samples - count) {
      return Err(pcm.Limit);
    }
    let (next, value) = d.decode_frame(input, at, limits);
    d = next;
    let frame = value?;
    if (frame.needs_history) {
      return Err(pcm.Header);
    }
    rate = frame.sample_rate;
    cc = frame.channels;
    for sample in frame.samples {
      samples = samples.append(sample);
    }
    at += frame.bytes_read;
  }
  if (rate == 0) {
    return Err(pcm.Truncated);
  }
  return Ok(Decoded { sample_rate: rate, channels: cc, samples: samples });
}

pub struct Track { sample_rate: i32, channels: i32, data_offset: i32, data_end: i32, declared_frames: u32, skip_frames: i32, padding_frames: i32 }

function audio_end(input: [u8]): Result[i32, pcm.AudioError] {
  let end = input.len();
  if (end >= 128 && wire.tag(input, end - 128, "TAG")) {
    end -= 128;
  }
  return ape_start(input, end);
}

function ape_start(input: [u8], own end: i32): Result[i32, pcm.AudioError] {
  if (end >= 32 && wire.tag(input, end - 32, "APETAGEX")) {
    let at = end - 32;
    let version = wire.le(input, at + 8, 4);
    let size = wire.le(input, at + 12, 4);
    let flags = wire.le(input, at + 20, 4);
    if (version != 1000u64 && version != 2000u64 || size < 32u64 || size > end as u64) {
      return Err(pcm.Header);
    }
    end -= size as i32;
    if ((flags & 2147483648u64) != 0u64) {
      if (end < 32 || !wire.tag(input, end - 32, "APETAGEX")) {
        return Err(pcm.Header);
      }
      end -= 32;
    }
  }
  return Ok(end);
}

function xing_fields(input: [u8], start: i32, end: i32, flags: u32): Result[(i32, u32), pcm.AudioError] {
  if ((flags & 4294967280u32) != 0u32) {
    return Err(pcm.Header);
  }
  let cursor = start;
  let declared = 0u32;
  for flag in [1, 2, 4, 8] {
    if ((flags & flag as u32) != 0u32) {
      let size = if (flag == 4) { 100 } else { 4 };
      if (size > end - cursor) {
        return Err(pcm.Header);
      }
      if (flag == 1) {
        declared = wire.be(input, cursor, 4) as u32;
        if (declared == 0u32) {
          return Err(pcm.Header);
        }
      }
      cursor += size;
    }
  }
  return Ok((cursor, declared));
}

function gapless_fields(input: [u8], cursor: i32, end: i32): Result[(i32, i32), pcm.AudioError] {
  if (wire.tag(input, cursor, "LAME") || wire.tag(input, cursor, "Lavc") || wire.tag(input, cursor, "Lavf")) {
    if (end - cursor < 36) {
      return Err(pcm.Header);
    }
    let delay = input[cursor + 21] as i32 << 4 | input[cursor + 22] as i32 >> 4;
    let pad = (input[cursor + 22] as i32 & 15) << 8 | input[cursor + 23] as i32;
    let padding = if (pad > DECODER_DELAY) { pad - DECODER_DELAY } else { 0 };
    return Ok((delay + DECODER_DELAY, padding));
  }
  return Ok((0, 0));
}

function xing_side(input: [u8], at: i32, h: header.Header, tag: i32): Result[void, pcm.AudioError] {
  let side_start = at + 4 + (if (h.crc) { 2 } else { 0 });
  if (h.crc && crc16(input[side_start:tag], crc16(input[at + 2:at + 4], 65535)) != wire.be(input, at + 4, 2) as i32) {
    return Err(pcm.Checksum);
  }
  let side = spectral.side_info(input[side_start:tag], h)?;
  if (side.begin != 0) {
    return Err(pcm.Header);
  }
  for g in side.granules {
    if (g.part != 0 || g.big != 0) {
      return Err(pcm.Header);
    }
  }
  return Ok(());
}

function track_header(input: [u8], at: i32, end: i32, limits: pcm.Limits): Result[(header.Header, i32), pcm.AudioError] {
  if (at > end) {
    return Err(pcm.Header);
  }
  let h = inspect(input, at, limits)?;
  if (h.bytes > end - at) {
    return Err(pcm.Truncated);
  }
  let side_start = at + 4 + (if (h.crc) { 2 } else { 0 });
  let tag = side_start + header.side_bytes(h);
  if (tag > at + h.bytes) {
    return Err(pcm.Header);
  }
  return Ok((h, tag));
}

pub function inspect_track(input: [u8], limits: pcm.Limits): Result[Track, pcm.AudioError] {
  let at = audio_start(input, limits)?;
  let end = audio_end(input)?;
  let (h, tag) = track_header(input, at, end, limits)?;
  let declared = 0u32;
  let skip = 0;
  let padding = 0;
  let data_at = at;
  if (at + h.bytes - tag >= 8 && (wire.tag(input, tag, "Xing") || wire.tag(input, tag, "Info"))) {
    xing_side(input, at, h, tag)?;
    let (cursor, count) = xing_fields(input, tag + 8, at + h.bytes, wire.be(input, tag + 4, 4) as u32)?;
    declared = count;
    let (encoder_skip, encoder_padding) = gapless_fields(input, cursor, at + h.bytes)?;
    skip = encoder_skip;
    padding = encoder_padding;
    data_at += h.bytes;
  }
  return Ok(Track { sample_rate: h.sample_rate, channels: h.channels, data_offset: data_at, data_end: end, declared_frames: declared, skip_frames: skip, padding_frames: padding });
}

// Gapless decode checks frame totals before copying the trimmed sample interval.
// fern-lint: allow cyclomatic-complexity
pub function decode(input: [u8], limits: pcm.Limits): Result[Decoded, pcm.AudioError] {
  let track = inspect_track(input, limits)?;
  let at = track.data_offset;
  let frames = 0u64;
  let samples = 0i64;
  while (at < track.data_end) {
    let h = inspect(input, at, limits)?;
    if (h.sample_rate != track.sample_rate || h.channels != track.channels) {
      return Err(pcm.Unsupported);
    }
    if (h.bytes > track.data_end - at) {
      return Err(pcm.Truncated);
    }
    samples += h.frames as i64 * (h.channels as i64);
    if (samples > limits.max_samples as i64) {
      return Err(pcm.Limit);
    }
    frames += 1u64;
    at += h.bytes;
  }
  if (frames == 0u64) {
    return Err(pcm.Truncated);
  }
  if (track.declared_frames != 0u32 && frames != track.declared_frames as u64) {
    return Err(pcm.Header);
  }
  let skip = track.skip_frames * track.channels;
  let padding = track.padding_frames * track.channels;
  if ((skip + padding) as i64 > samples) {
    return Err(pcm.Header);
  }
  let end = samples as i32 - padding;
  let output = spectral.zeros(end - skip);
  let d = decoder();
  let scratch = spectral.zeros(2304);
  let absolute = 0;
  let written = 0;
  at = track.data_offset;
  while (at < track.data_end) {
    let (next, result) = d.decode_frame_into(input, at, scratch, limits);
    d = next;
    let frame = result?;
    if (frame.needs_history) {
      return Err(pcm.Header);
    }
    let Frame { samples: buffer, frames: frame_count, channels, bytes_read, sample_rate: _, needs_history: _ } = frame;
    let count = frame_count * channels;
    for i in 0..count {
      if (absolute >= skip && absolute < end) {
        output = output.with(written, buffer[i]);
        written += 1;
      }
      absolute += 1;
    }
    scratch = buffer;
    at += bytes_read;
  }
  return Ok(Decoded { sample_rate: track.sample_rate, channels: track.channels, samples: output });
}
```
