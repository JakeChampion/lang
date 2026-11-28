# Native ALAC packet decoder

This module decodes Apple Lossless version-zero packets to exact signed integer PCM.
Configuration declares sample depth, frame length, sample rate, and channel layout.
Samples use WAVE speaker order and retain their declared right-aligned depth.
The information record includes the speaker mask.

Create a decoder from a 24-byte configuration or a 48-byte configuration with a channel-layout extension.
Decoder state retains two channel scratch arrays and two arrays of predictor taps. Packets need no history from earlier packets.
The reusable-output method preserves buffer entries after the valid prefix.
The valid prefix contains `frame.frames * frame.format.channels` samples.

The convenience method allocates exactly that prefix.
`packet_frames` reads the first audio-element header without residual decode.
`inspect_packet` performs the same check from configuration bytes without predictor scratch arrays.
It checks the input and sample budgets. A packet read validates all channels and the complete payload.

Errors consume decoder state. Input, frame, and output limits apply before allocation.
Compressed stereo with more than 32 predictor bits returns `Unsupported`.
This module is available under MPL-2.0. See `licenses/alac/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's ALAC decoder.
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/alac/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.

import "std/audio/alac/bits";
import "std/audio/alac/config";
import "std/audio/alac/entropy";
import "std/audio/alac/predict";
import "std/audio/pcm";

pub opaque struct Decoder { info: config.Info, work: Work }

pub struct Frame { format: pcm.Format, frames: i32, bytes_read: i32, samples: i32[] }

struct Work { first: i32[], second: i32[], first_taps: i32[], second_taps: i32[] }

// The coefficient count is a 5-bit field.
const MAX_TAPS: i32 = 31;

struct ElementHeader { at: i64, frames: i32, shift: i32, raw: boolean }

pub function create(configuration: [u8], limits: pcm.Limits): Result[Decoder, pcm.AudioError] {
  let info = config.read(configuration, limits)?;
  let stereo = info.format.channels > 1;
  let work = Work { first: pcm.zeros(info.frame_length), second: if (stereo) { pcm.zeros(info.frame_length) } else { [] }, first_taps: pcm.zeros(MAX_TAPS), second_taps: if (stereo) { pcm.zeros(MAX_TAPS) } else { [] } };
  return Ok(Decoder { info: info, work: work });
}

pub function (value: Decoder) information(): config.Info {
  return value.info;
}

function header(input: [u8], at: i64, info: config.Info): Result[ElementHeader, pcm.AudioError] {
  let (p1, instance) = bits.read(input, at, 16)?;
  if ((instance & 4095u32) != 0u32) {
    return Err(pcm.Header);
  }
  let (p2, flags) = bits.read(input, p1, 4)?;
  let shift = (flags >> 1u32 & 3u32) as i32 * 8;
  if (shift >= 24 || shift >= info.format.bits_per_sample) {
    return Err(pcm.Header);
  }
  at = p2;
  let count = info.frame_length;
  if ((flags & 8u32) != 0u32) {
    let (p3, size) = bits.read(input, at, 32)?;
    if (size < 1u32 || size > info.frame_length as u32) {
      return Err(pcm.Header);
    }
    at = p3;
    count = size as i32;
  }
  return Ok(ElementHeader { at: at, frames: count, shift: shift, raw: (flags & 1u32) != 0u32 });
}

function check_input(input: [u8], info: config.Info, limits: pcm.Limits): Result[void, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (info.max_frame_bytes > 0i64 && input.len() as i64 > info.max_frame_bytes) {
    return Err(pcm.Limit);
  }
  return Ok(());
}

function element_channels(tag: u32, available: i32): Result[i32, pcm.AudioError] {
  if (tag == 2u32 || tag == 5u32) {
    return Err(pcm.Unsupported);
  }
  let count = if (tag == 1u32) { 2 } else { 1 };
  if (tag == 7u32 || count > available) {
    return Err(pcm.Header);
  }
  return Ok(count);
}

pub function (value: Decoder) packet_frames(input: [u8], limits: pcm.Limits): Result[i32, pcm.AudioError] {
  return packet_frame_count(input, value.info, limits);
}

// Inspect a packet header without allocation of predictor scratch arrays.
pub function inspect_packet(input: [u8], configuration: [u8], limits: pcm.Limits): Result[i32, pcm.AudioError] {
  let info = config.read(configuration, limits)?;
  return packet_frame_count(input, info, limits);
}

function packet_frame_count(input: [u8], info: config.Info, limits: pcm.Limits): Result[i32, pcm.AudioError] {
  check_input(input, info, limits)?;
  let at = 0i64;
  while (true) {
    let (next, tag) = bits.read(input, at, 3)?;
    at = next;
    if (tag == 4u32 || tag == 6u32) {
      at = ancillary(input, at, tag)?;
      continue;
    }
    let _ = element_channels(tag, info.format.channels)?;
    let fields = header(input, at, info)?;
    let _ = pcm.sample_count(fields.frames, info.format.channels, limits)?;
    return Ok(fields.frames);
  }
}

// Raw samples already carry their signed PCM width. Write them in channel order.
function raw_element(input: [u8], info: config.Info, fields: ElementHeader, count: i32, first_channel: i32, second_channel: i32, own output: i32[]): Result[(i64, i32[]), pcm.AudioError] {
  let at = fields.at;
  let width = info.format.bits_per_sample as i64;
  let end = bits.skip(input, at, fields.frames as i64 * (count as i64) * width)?;
  let unused = 64i64 - width;
  for i in 0..fields.frames {
    let start = i * info.format.channels;
    output = output.with(start + first_channel, (bits.window(input, at) as i64 >> unused) as i32);
    at += width;
    if (count == 2) {
      output = output.with(start + second_channel, (bits.window(input, at) as i64 >> unused) as i32);
      at += width;
    }
  }
  return Ok((end, output));
}

// Residuals, prediction, and low bits share one checked element extent.
// fern-lint: allow cyclomatic-complexity
function compressed_element(input: [u8], info: config.Info, paired: boolean, fields: ElementHeader, own work: Work): Result[(i64, Work), pcm.AudioError] {
  let at = fields.at;
  let count = fields.frames;
  let shift = fields.shift;
  let Work { first, second, first_taps, second_taps } = work;
  let depth = info.format.bits_per_sample - shift + (if (paired) { 1 } else { 0 });
  let (p4, mid_shift) = bits.read(input, at, 8)?;
  let (p5, weight) = bits.signed(input, p4, 8)?;
  if (!paired && (mid_shift != 0u32 || weight != 0) || weight != 0 && mid_shift > 31u32) {
    return Err(pcm.Header);
  }
  let (p6, parameters) = entropy.parameters(input, p5, info, depth)?;
  let (p7, read_taps) = entropy.coefficients(input, p6, parameters, first_taps)?;
  first_taps = read_taps;
  at = p7;
  let other_parameters: Option[entropy.Parameters] = None;
  if (paired) {
    let (p8, other) = entropy.parameters(input, at, info, depth)?;
    let (p9, other_taps) = entropy.coefficients(input, p8, other, second_taps)?;
    second_taps = other_taps;
    at = p9;
    other_parameters = Some(other);
  }
  let tail = at;
  let channels = if (paired) { 2 } else { 1 };
  at = bits.skip(input, at, count as i64 * (channels as i64) * (shift as i64))?;
  let (p10, residuals) = entropy.residuals(input, at, info, parameters, first, count)?;
  let (restored, used_taps) = predict.restore(residuals, count, parameters, first_taps)?;
  first = restored;
  first_taps = used_taps;
  at = p10;
  if let Some(other) = other_parameters {
    let (p11, residual_pair) = entropy.residuals(input, at, info, other, second, count)?;
    let (restored_pair, used_pair_taps) = predict.restore(residual_pair, count, other, second_taps)?;
    second = restored_pair;
    second_taps = used_pair_taps;
    at = p11;
    if (weight != 0) {
      for i in 0..count {
        let left = first[i] + second[i] - (second[i] * weight >> mid_shift as i32);
        let right = left - second[i];
        first = first.with(i, left);
        second = second.with(i, right);
      }
    }
  }
  if (shift > 0) {
    // The skip above checked every low bit. Both channel fields fit in one window.
    let low = tail;
    let stride = (channels * shift) as i64;
    let high = (64 - shift) as u64;
    for i in 0..count {
      let pair = bits.window(input, low);
      first = first.with(i, first[i] << shift | (pair >> high) as i32);
      if (paired) {
        second = second.with(i, second[i] << shift | (pair << shift as u64 >> high) as i32);
      }
      low += stride;
    }
  }
  return Ok((at, Work { first: first, second: second, first_taps: first_taps, second_taps: second_taps }));
}

// Data and fill elements have distinct bounded length and alignment fields.
// fern-lint: allow cyclomatic-complexity
function ancillary(input: [u8], at: i64, tag: u32): Result[i64, pcm.AudioError] {
  if (tag == 4u32) {
    let (p1, _) = bits.read(input, at, 4)?;
    let (p2, align) = bits.read(input, p1, 1)?;
    let (p3, size) = bits.read(input, p2, 8)?;
    at = p3;
    let count = size;
    if (size == 255u32) {
      let (p4, extra) = bits.read(input, at, 8)?;
      at = p4;
      count += extra;
    }
    if (align != 0u32) {
      at = bits.skip(input, at, (8i64 - at % 8i64) % 8i64)?;
    }
    return bits.skip(input, at, count as i64 * 8i64);
  }
  let (p5, size) = bits.read(input, at, 4)?;
  at = p5;
  let count = size;
  if (size == 15u32) {
    let (p6, extra) = bits.read(input, at, 8)?;
    at = p6;
    count += extra - 1u32;
  }
  return bits.skip(input, at, count as i64 * 8i64);
}

function ended(input: [u8], at: i64): Result[void, pcm.AudioError] {
  let padding = ((8i64 - at % 8i64) % 8i64) as i32;
  let (end, value) = bits.read(input, at, padding)?;
  if (value != 0u32 || end != input.len() as i64 * 8i64) {
    return Err(pcm.Header);
  }
  return Ok(());
}

// Preserve element order, equal frame lengths, output bounds, and the mandatory end marker.
// fern-lint: allow cyclomatic-complexity
pub function (own value: Decoder) decode_packet_into(input: [u8], own output: i32[], limits: pcm.Limits): Result[(Decoder, Frame), pcm.AudioError] {
  check_input(input, value.info, limits)?;
  let Decoder { info, work } = value;
  let at = 0i64;
  let channel = 0;
  let frames = 0;
  while (true) {
    let (next, tag) = bits.read(input, at, 3)?;
    at = next;
    if (tag == 7u32) {
      if (channel != info.format.channels || frames < 1) {
        return Err(pcm.Header);
      }
      ended(input, at)?;
      return Ok((Decoder { info: info, work: work }, Frame { format: info.format, frames: frames, bytes_read: input.len(), samples: output }));
    }
    if (tag == 4u32 || tag == 6u32) {
      at = ancillary(input, at, tag)?;
      continue;
    }
    let count = element_channels(tag, info.format.channels - channel)?;
    let paired = count == 2;
    let fields = header(input, at, info)?;
    if (frames != 0 && frames != fields.frames) {
      return Err(pcm.Header);
    }
    let needed = pcm.sample_count(fields.frames, info.format.channels, limits)?;
    if (needed > output.len()) {
      return Err(pcm.Buffer);
    }
    frames = fields.frames;
    let first_channel = config.channel(info.format.channels, channel)?;
    let second_channel = if (paired) { config.channel(info.format.channels, channel + 1)? } else { first_channel };
    if (fields.raw) {
      let (next_at, samples) = raw_element(input, info, fields, count, first_channel, second_channel, output)?;
      output = samples;
      at = next_at;
      channel += count;
      continue;
    }
    let (after_element, next_work) = compressed_element(input, info, paired, fields, work)?;
    work = next_work;
    at = after_element;
    let clip = 32 - info.format.bits_per_sample;
    if (paired) {
      for i in 0..frames {
        let start = i * info.format.channels;
        output = output.with(start + first_channel, work.first[i] << clip >> clip);
        output = output.with(start + second_channel, work.second[i] << clip >> clip);
      }
    } else {
      for i in 0..frames {
        output = output.with(i * info.format.channels + first_channel, work.first[i] << clip >> clip);
      }
    }
    channel += count;
  }
}

pub function (own value: Decoder) decode_packet(input: [u8], limits: pcm.Limits): Result[(Decoder, Frame), pcm.AudioError] {
  let frames = value.packet_frames(input, limits)?;
  let count = pcm.sample_count(frames, value.info.format.channels, limits)?;
  return value.decode_packet_into(input, pcm.zeros(count), limits);
}
```
