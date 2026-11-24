# MPEG Layer I playback

`decode_raw` decodes consecutive indexed-bitrate or free-format MPEG-1 or MPEG-2 Layer I frames to interleaved float PCM. Mono, stereo, dual-channel and joint-stereo streams retain synthesis history between compatible frames. Protected frames check the header and allocation fields before reading scalefactors. Input and cumulative sample counts use the caller's PCM limits. Tags and de-emphasis filtering are outside this entry point.

A fresh free-format frame needs the compatible header that follows it, or a second complete frame at the end of the input. The extent is a whole number of four-byte slots and must not exceed `max_input_bytes`. The decoder keeps that extent, so later frames and the final frame need no lookahead.

For incremental playback, create a `decoder()` and pass a byte view starting at each frame to `decode_frame_into`. Each success consumes `bytes_read` input bytes and writes 384 samples per channel to the supplied output prefix. The caller may recycle the returned array. Errors preserve the last successful synthesis history. Start a new decoder for a different rate or channel count.

`seek_index` indexes frame headers in an immutable byte view. Pass the same input to `read_into` to read a range in PCM frames. The index retains frame offsets and the free-format extent. Reads check the payloads and CRCs of the requested frames and their synthesis preroll. Samples beyond the requested output prefix are preserved.

```fern
<<*>>=
// Decode bounded MPEG Layer I frames and streams into interleaved PCM.

import "std/audio/mp2/layer";
import "std/audio/mp2/tables";
import "std/audio/mp3/bits";
import "std/audio/mp3/framing";
import "std/audio/mp3/spectral";
import "std/audio/mp3/synthesis";
import "std/audio/pcm";
import "std/audio/mpeg/seek";
import "std/audio/wire";
import "std/i32";

pub struct Decoded { sample_rate: i32, channels: i32, samples: f32[] }

pub opaque struct Decoder { rate: i32, channels: i32, free: i32, history: f32[], bands: f32[], lines: f32[], transform: f32[] }

pub struct Frame { sample_rate: i32, channels: i32, bytes_read: i32, samples: f32[] }

pub function decoder(): Decoder {
  return Decoder { rate: 0, channels: 0, free: 0, history: spectral.zeros(960), bands: spectral.zeros(1152), lines: spectral.zeros(1728), transform: spectral.zeros(128) };
}

struct Header { rate: i32, channels: i32, separate: i32, bytes: i32, free: i32, crc: boolean }

const RATES: i32[] = [44100, 48000, 32000];

const LOW_BITRATES: i32[] = [0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256];

function invalid_header(input: [u8], a: i32, version: i32, rate_index: i32, bitrate_index: i32): boolean {
  return input[0] != 255u8 || a >> 5 != 7 || version == 1 || (a & 6) == 0 || rate_index == 3 || bitrate_index == 15 || (input[3] & 3u8) == 2u8;
}

function frame_length(bitrate: i32, rate: i32, base: i32, padding: i32): i32 {
  if (bitrate != 0) {
    return 4 * (12000 * bitrate / rate + padding);
  }
  return if (base > 0) { base + 4 * padding } else { 0 };
}

function separate_bands(mode: i32, flags: u8): i32 {
  return if (mode == 1) { 4 * (1 + (flags as i32 >> 4 & 3)) } else { 32 };
}

// A free-format header has zero bytes until its unpadded extent is known.
function parsed(input: [u8], free: i32, maximum: i32): Result[Header, pcm.AudioError] {
  if (input.len() < 4) {
    return Err(pcm.Truncated);
  }
  let a = input[1] as i32;
  let b = input[2] as i32;
  let mode = input[3] as i32 >> 6;
  let version = a >> 3 & 3;
  let rate_index = b >> 2 & 3;
  let bitrate_index = b >> 4;
  if (invalid_header(input, a, version, rate_index, bitrate_index)) {
    return Err(pcm.Header);
  }
  if (version == 0 || (a & 6) != 6) {
    return Err(pcm.Unsupported);
  }
  let rate = RATES[rate_index] / (if (version == 3) { 1 } else { 2 });
  let bitrate = if (version == 3) { bitrate_index * 32 } else { LOW_BITRATES[bitrate_index] };
  let padding = b >> 1 & 1;
  let crc = (a & 1) == 0;
  let base = if (bitrate_index == 0) { free } else { 0 };
  if (base > maximum) {
    return Err(pcm.Limit);
  }
  let bytes = frame_length(bitrate, rate, base, padding);
  return Ok(Header { rate: rate, channels: if (mode == 3) { 1 } else { 2 }, separate: separate_bands(mode, input[3]), bytes: bytes, free: base, crc: crc });
}

// Return the encoded frame length from its header, or zero when a free-format
// frame needs lookahead. A known free extent excludes the four-byte padding slot.
pub function frame_bytes(input: [u8], free: i32, maximum: i32): Result[i32, pcm.AudioError] {
  if (free < 0 || free % 4 != 0 || free > 1073741820) {
    return Err(pcm.Header);
  }
  let h = parsed(input, free, maximum)?;
  return Ok(h.bytes);
}

function header(input: [u8], free: i32, maximum: i32): Result[Header, pcm.AudioError] {
  let h = parsed(input, free, maximum)?;
  if (h.bytes == 0) {
    let search = framing.layered(3, input[1] as i32 >> 3 & 3, input[2] as i32 >> 2 & 3, h.channels == 1, input[2] as i32 >> 1 & 1, h.crc, 0);
    let base = framing.discover(input, search, maximum)?;
    h = parsed(input, base, maximum)?;
  }
  if (h.bytes > input.len()) {
    return Err(pcm.Truncated);
  }
  return Ok(h);
}

// Decode a frame whose byte extent was established by a container or stream reader.
pub function (own state: Decoder) decode_packet_into(packet: [u8], own output: f32[], limits: pcm.Limits): (Decoder, Result[Frame, pcm.AudioError]) {
  if (packet.len() < 4) {
    return (state, Err(pcm.Truncated));
  }
  let free = state.free;
  if (packet[2] >> 4u8 == 0u8 && free == 0) {
    free = packet.len() - (packet[2] as i32 >> 1 & 1) * 4;
  }
  let size = match (frame_bytes(packet, free, limits.max_input_bytes)) { Ok(n) => n, Err(e) => { return (state, Err(e)); } };
  if (size < 4 + (if ((packet[1] & 1u8) == 0u8) { 2 } else { 0 }) || size != packet.len()) {
    return (state, Err(pcm.Header));
  }
  let before = state.free;
  let (next, result) = Decoder { ...state, free: free }.decode_frame_into(packet, output, limits);
  if let Err(e) = result {
    return (Decoder { ...next, free: before }, Err(e));
  }
  return (next, result);
}

function allocation(data: [u8], h: Header): Result[(bits.Bits, u8[]), pcm.AudioError] {
  let b = bits.new(data.len() * 8);
  let widths = __alloc_u8(64);
  for band in 0..32 {
    let width = 0;
    for ch in 0..h.channels {
      if (ch == 0 || band < h.separate) {
        let (next, value) = bits.read(data, b, 4);
        b = next;
        if (b.bad) {
          return Err(pcm.Truncated);
        }
        if (value == 15) {
          return Err(pcm.Header);
        }
        width = if (value == 0) { 0 } else { value + 1 };
      }
      widths = widths.with(2 * band + ch, width as u8);
    }
  }
  return Ok((b, widths));
}

// Sample fields are 2–15 bits. Their complete extent is checked once
// before decoding the twelve slots. Read only bytes that belong to the field.
function sample_code(data: [u8], pos: i32, width: i32): i32 {
  let at = pos >> 3;
  let end = (pos & 7) + width;
  let value = data[at] as i32 << 16;
  if (end > 8) {
    value |= data[at + 1] as i32 << 8;
  }
  if (end > 16) {
    value |= data[at + 2] as i32;
  }
  return value >> 24 - end & (1 << width) - 1;
}

function scales_for(frame: [u8], data: [u8], h: Header): Result[(i32, u8[], f32[]), pcm.AudioError] {
  let (b, widths) = allocation(data, h)?;
  if (h.crc && layer.checksum(frame[2:4], data, b.pos) != wire.be(frame, 4, 2) as i32) {
    return Err(pcm.Checksum);
  }
  let scales = spectral.zeros(64);
  let slot_bits = 0;
  for i in 0..64 {
    let width = widths[i] as i32;
    if (width != 0) {
      if ((i & 1) == 0 || i / 2 < h.separate) {
        slot_bits += width;
      }
      let (next, index) = bits.read(data, b, 6);
      b = next;
      let factor = tables.DEQUANT[3 * (width - 2) + index % 3] * ((2097152 >> index / 3) as f32);
      scales = scales.with(i, factor);
    }
  }
  if (b.bad || slot_bits * 12 > b.limit - b.pos) {
    return Err(pcm.Truncated);
  }
  return Ok((b.pos, widths, scales));
}

function decode_slots(data: [u8], h: Header, widths: [u8], scales: [f32], pos: i32, own samples: f32[]): f32[] {
  for slot in 0..12 {
    for band in 0..32 {
      let value = 0;
      for ch in 0..h.channels {
        let i = 2 * band + ch;
        let width = widths[i] as i32;
        let sample = 0f32;
        if (width > 0) {
          if (ch == 0 || band < h.separate) {
            let code = sample_code(data, pos, width);
            pos += width;
            value = code - ((1 << width - 1) - 1);
          }
          sample = value as f32 * scales[i];
        }
        samples = samples.with(ch * 576 + band * 18 + slot, sample);
      }
    }
  }
  return samples;
}

function render(own state: Decoder, input: [u8], h: Header, own output: f32[]): (Decoder, Result[Frame, pcm.AudioError]) {
  if (state.rate != 0 && (h.rate != state.rate || h.channels != state.channels)) {
    return (state, Err(pcm.Header));
  }
  let frame = input[:h.bytes];
  let data = frame[if (h.crc) { 6 } else { 4 }:];
  let (pos, widths, scales) = match (scales_for(frame, data, h)) { Ok(value) => value, Err(e) => { return (state, Err(e)); } };
  let Decoder { rate, channels, free, history, bands, lines, transform } = state;
  bands = decode_slots(data, h, widths, scales, pos, bands);
  let (next, samples, next_bands, next_lines, next_transform) = synthesis.granule12_into(history, bands, h.channels, output, 0, lines, transform);
  return (Decoder { rate: h.rate, channels: h.channels, free: if (h.free > 0) { h.free } else { free }, history: next, bands: next_bands, lines: next_lines, transform: next_transform }, Ok(Frame { sample_rate: h.rate, channels: h.channels, bytes_read: h.bytes, samples: samples }));
}

pub function (own state: Decoder) decode_frame_into(input: [u8], own output: f32[], limits: pcm.Limits): (Decoder, Result[Frame, pcm.AudioError]) {
  if let Err(e) = pcm.check_input(input, limits) {
    return (state, Err(e));
  }
  let h = match (header(input, state.free, limits.max_input_bytes)) { Ok(value) => value, Err(e) => { return (state, Err(e)); } };
  let count = match (pcm.sample_count(384, h.channels, limits)) { Ok(value) => value, Err(e) => { return (state, Err(e)); } };
  if (output.len() < count) {
    return (state, Err(pcm.Buffer));
  }
  return render(state, input, h, output);
}

pub function decode_raw(input: [u8], limits: pcm.Limits): Result[Decoded, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  let first = header(input, 0, limits.max_input_bytes)?;
  let count = pcm.sample_count(384, first.channels, limits)?;
  let state = decoder();
  let frame = spectral.zeros(count);
  let samples: f32[] = [];
  let at = 0;
  while (at < input.len()) {
    let h = header(input[at:], state.free, limits.max_input_bytes)?;
    if (h.channels != first.channels || h.rate != first.rate) {
      return Err(pcm.Header);
    }
    if (count > limits.max_samples - samples.len()) {
      return Err(pcm.Limit);
    }
    let (next, result) = render(state, input[at:], h, frame);
    state = next;
    let decoded = result?;
    for sample in decoded.samples {
      samples = samples.append(sample);
    }
    frame = decoded.samples;
    at += h.bytes;
  }
  return Ok(Decoded { sample_rate: first.rate, channels: first.channels, samples: samples });
}

pub opaque struct SeekIndex { index: seek.Index }

function seek_frame(input: [u8], free: i32, limits: pcm.Limits): Result[seek.Frame, pcm.AudioError] {
  let h = header(input, free, limits.max_input_bytes)?;
  return Ok(seek.Frame { rate: h.rate, channels: h.channels, bytes: h.bytes, free: free.max(h.free) });
}

pub function seek_index(input: [u8], max_entries: i32, limits: pcm.Limits): Result[SeekIndex, pcm.AudioError] {
  return Ok(SeekIndex { index: seek.build(input, 384, max_entries, limits, seek_frame)? });
}

pub function (value: SeekIndex) frames(): i64 {
  return value.index.frames();
}

pub function (value: SeekIndex) read_into(input: [u8], position: i64, frames: i32, own output: f32[]): Result[f32[], pcm.AudioError] {
  let index = value.index;
  let range = index.range(input.len(), position, frames, output.len())?;
  if (range.count == 0) {
    return Ok(output);
  }
  let state = Decoder { ...decoder(), free: index.free };
  let scratch = spectral.zeros(384 * index.channels);
  let written = 0;
  for n in range.warmup..range.last + 1 {
    let start = index.offsets[n];
    let end = index.end(n);
    let (next, result) = state.decode_frame_into(input[start:end], scratch, index.limits);
    let decoded = result?;
    if (decoded.bytes_read != end - start || decoded.channels != index.channels || decoded.sample_rate != index.rate) {
      return Err(pcm.Header);
    }
    state = next;
    scratch = decoded.samples;
    let (after, filled) = range.copy(n, scratch, written, output);
    written = after;
    output = filled;
  }
  return Ok(output);
}
```
