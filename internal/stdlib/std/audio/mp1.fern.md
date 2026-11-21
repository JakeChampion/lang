# MPEG Layer I playback

`decode_raw` decodes consecutive indexed-bitrate MPEG-1 or MPEG-2 Layer I frames to interleaved float PCM. Mono, stereo, dual-channel and joint-stereo streams retain synthesis history between compatible frames. Protected frames check the header and allocation fields before reading scalefactors. Input and cumulative sample counts use the caller's PCM limits. Tags, free-format frame discovery and de-emphasis filtering are outside this entry point.

For incremental playback, create a `decoder()` and pass a byte view starting at each frame to `decode_frame_into`. Each success consumes `bytes_read` input bytes and writes 384 samples per channel to the supplied output prefix. The caller may recycle the returned array. Errors preserve the last successful synthesis history. Start a new decoder for a different rate or channel count.

```fern
<<*>>=
// Decode bounded MPEG Layer I frames and streams into interleaved PCM.

import "std/audio/mp2/layer";
import "std/audio/mp2/tables";
import "std/audio/mp3/bits";
import "std/audio/mp3/spectral";
import "std/audio/mp3/synthesis";
import "std/audio/pcm";
import "std/audio/wire";

pub struct Decoded { sample_rate: i32, channels: i32, samples: f32[] }

pub opaque struct Decoder { rate: i32, channels: i32, history: f32[], bands: f32[], lines: f32[], transform: f32[] }

pub struct Frame { sample_rate: i32, channels: i32, bytes_read: i32, samples: f32[] }

pub function decoder(): Decoder {
  return Decoder { rate: 0, channels: 0, history: spectral.zeros(960), bands: spectral.zeros(1152), lines: spectral.zeros(1728), transform: spectral.zeros(128) };
}

struct Header { rate: i32, channels: i32, separate: i32, bytes: i32, crc: boolean }

const RATES: i32[] = [44100, 48000, 32000];

const LOW_BITRATES: i32[] = [0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256];

function invalid_header(input: [u8], a: i32, version: i32, rate_index: i32, bitrate_index: i32): boolean {
  return input[0] != 255u8 || a >> 5 != 7 || version == 1 || (a & 6) == 0 || rate_index == 3 || bitrate_index == 15 || (input[3] & 3u8) == 2u8;
}

function header(input: [u8]): Result[Header, pcm.AudioError] {
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
  if (version == 0 || (a & 6) != 6 || bitrate_index == 0) {
    return Err(pcm.Unsupported);
  }
  let rate = RATES[rate_index] / (if (version == 3) { 1 } else { 2 });
  let bitrate = if (version == 3) { bitrate_index * 32 } else { LOW_BITRATES[bitrate_index] };
  let bytes = 4 * (12000 * bitrate / rate + (b >> 1 & 1));
  if (bytes > input.len()) {
    return Err(pcm.Truncated);
  }
  return Ok(Header { rate: rate, channels: if (mode == 3) { 1 } else { 2 }, separate: if (mode == 1) { 4 * (1 + (input[3] as i32 >> 4 & 3)) } else { 32 }, bytes: bytes, crc: (a & 1) == 0 });
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
  let Decoder { rate, channels, history, bands, lines, transform } = state;
  bands = decode_slots(data, h, widths, scales, pos, bands);
  let (next, samples, next_bands, next_lines, next_transform) = synthesis.granule12_into(history, bands, h.channels, output, 0, lines, transform);
  return (Decoder { rate: h.rate, channels: h.channels, history: next, bands: next_bands, lines: next_lines, transform: next_transform }, Ok(Frame { sample_rate: h.rate, channels: h.channels, bytes_read: h.bytes, samples: samples }));
}

pub function (own state: Decoder) decode_frame_into(input: [u8], own output: f32[], limits: pcm.Limits): (Decoder, Result[Frame, pcm.AudioError]) {
  if let Err(e) = pcm.check_input(input, limits) {
    return (state, Err(e));
  }
  let h = match (header(input)) { Ok(value) => value, Err(e) => { return (state, Err(e)); } };
  let count = match (pcm.sample_count(384, h.channels, limits)) { Ok(value) => value, Err(e) => { return (state, Err(e)); } };
  if (output.len() < count) {
    return (state, Err(pcm.Buffer));
  }
  return render(state, input, h, output);
}

pub function decode_raw(input: [u8], limits: pcm.Limits): Result[Decoded, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  let first = header(input)?;
  let count = pcm.sample_count(384, first.channels, limits)?;
  let state = decoder();
  let frame = spectral.zeros(count);
  let samples: f32[] = [];
  let at = 0;
  while (at < input.len()) {
    let h = header(input[at:])?;
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
```
