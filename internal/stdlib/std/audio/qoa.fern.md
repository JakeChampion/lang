# Quite OK Audio

QOA is the audio counterpart of QOI. It uses lossy, fixed-rate compression with a four-tap predictor.
This module supports one through eight channels, 16-bit PCM, and static QOA files.

The encoder follows the reference scalefactor search, error score, and predictor update.
Each frame contains at most 5120 samples per channel. Frame state permits independent decode and seek.

`decode_frame` accepts a frame at a byte offset. It does not require the file header.
`decode_frame_into` consumes a reusable output buffer. Only the first `frames * format.channels` entries contain output.

The caller must discard the result on error. No decoder state changes before a complete frame passes validation.
`encode_frame` consumes encoder state. `encode` and `decode` provide bounded whole-file convenience functions.

Static files require full frames before the final frame and consistent channel counts and sample rates.
Streaming headers with an unknown total sample count return `Unsupported` from `inspect`.

The implementation derives from [qoa.h](https://github.com/phoboslab/qoa), by Dominic Szablewski.
The source retains the MIT license below.

```fern
<<*>>=
// Copyright (c) 2023, Dominic Szablewski - https://phoboslab.org
// SPDX-License-Identifier: MIT
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.
import "std/audio/pcm";
import "std/audio/wire";

pub const FRAME_LEN: i32 = 5120;

pub struct Header { format: pcm.Format, frames: u32 }

pub struct Frame { format: pcm.Format, frames: i32, bytes_read: i32, samples: i32[] }

pub struct Lms { h0: i32, h1: i32, h2: i32, h3: i32, w0: i32, w1: i32, w2: i32, w3: i32 }

pub struct Encoder { format: pcm.Format, state: Lms[] }

pub struct EncodedFrame { encoder: Encoder, bytes: u8[] }

struct FrameHeader { format: pcm.Format, frames: i32, size: i32 }

const RECIPROCAL: i32[] = [65536, 9363, 3121, 1457, 781, 475, 311, 216, 156, 117, 90, 71, 57, 47, 39, 32];

const QUANT: i32[] = [7, 7, 7, 5, 5, 3, 3, 1, 0, 0, 2, 2, 4, 4, 6, 6, 6];

const DEQUANT: i32[] = [1, -1, 3, -3, 5, -5, 7, -7, 5, -5, 18, -18, 32, -32, 49, -49, 16, -16, 53, -53, 95, -95, 147, -147, 34, -34, 113, -113, 203, -203, 315, -315, 63, -63, 210, -210, 378, -378, 588, -588, 104, -104, 345, -345, 621, -621, 966, -966, 158, -158, 528, -528, 950, -950, 1477, -1477, 228, -228, 760, -760, 1368, -1368, 2128, -2128, 316, -316, 1053, -1053, 1895, -1895, 2947, -2947, 422, -422, 1405, -1405, 2529, -2529, 3934, -3934, 548, -548, 1828, -1828, 3290, -3290, 5117, -5117, 696, -696, 2320, -2320, 4176, -4176, 6496, -6496, 868, -868, 2893, -2893, 5207, -5207, 8099, -8099, 1064, -1064, 3548, -3548, 6386, -6386, 9933, -9933, 1286, -1286, 4288, -4288, 7718, -7718, 12005, -12005, 1536, -1536, 5120, -5120, 9216, -9216, 14336, -14336];

function clamp(v: i32, low: i32, high: i32): i32 {
  if (v < low) {
    return low;
  }
  if (v > high) {
    return high;
  }
  return v;
}

function predict(s: Lms): i32 {
  return s.w0 * s.h0 + s.w1 * s.h1 + s.w2 * s.h2 + s.w3 * s.h3 >> 13;
}

function update(s: Lms, sample: i32, residual: i32): Lms {
  let delta = residual >> 4;
  return Lms {
    h0: s.h1, h1: s.h2, h2: s.h3, h3: sample,
    w0: s.w0 + (if (s.h0 < 0) { -delta } else { delta }),
    w1: s.w1 + (if (s.h1 < 0) { -delta } else { delta }),
    w2: s.w2 + (if (s.h2 < 0) { -delta } else { delta }),
    w3: s.w3 + (if (s.h3 < 0) { -delta } else { delta }),
  };
}

function signed16(value: u64): i32 {
  return value as i32 << 16 >> 16;
}

function read_lms(input: [u8], at: i32): Lms {
  return Lms {
    h0: signed16(wire.be(input, at, 2)), h1: signed16(wire.be(input, at + 2, 2)),
    h2: signed16(wire.be(input, at + 4, 2)), h3: signed16(wire.be(input, at + 6, 2)),
    w0: signed16(wire.be(input, at + 8, 2)), w1: signed16(wire.be(input, at + 10, 2)),
    w2: signed16(wire.be(input, at + 12, 2)), w3: signed16(wire.be(input, at + 14, 2)),
  };
}

function frame_header(input: [u8], at: i32, limits: pcm.Limits): Result[FrameHeader, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (at < 0 || at > input.len()) {
    return Err(pcm.Header);
  }
  if (input.len() - at < 8) {
    return Err(pcm.Truncated);
  }
  let channels = input[at] as i32;
  let format = pcm.Format { sample_rate: wire.be(input, at + 1, 3) as i32, channels: channels, bits_per_sample: 16 };
  let frames = wire.be(input, at + 4, 2) as i32;
  let size = wire.be(input, at + 6, 2) as i32;
  if (!pcm.valid(format) || frames < 1 || frames > FRAME_LEN) {
    return Err(pcm.Header);
  }
  let expected = 8 + 16 * channels + 8 * ((frames + 19) / 20) * channels;
  if (size != expected) {
    return Err(pcm.Header);
  }
  if (size > input.len() - at) {
    return Err(pcm.Truncated);
  }
  let _ = pcm.sample_count(frames, channels, limits)?;
  return Ok(FrameHeader { format: format, frames: frames, size: size });
}

pub function inspect(input: [u8], limits: pcm.Limits): Result[Header, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (input.len() < 16) {
    return Err(pcm.Truncated);
  }
  if (!wire.tag(input, 0, "qoaf")) {
    return Err(pcm.Header);
  }
  let frames = wire.be(input, 4, 4) as u32;
  if (frames == 0u32) {
    return Err(pcm.Unsupported);
  }
  let first = frame_header(input, 8, limits)?;
  return Ok(Header { format: first.format, frames: frames });
}

function decode_samples(input: [u8], at: i32, h: FrameHeader, own samples: i32[], output_at: i32): i32[] {
  let cc = h.format.channels;
  let states: Lms[] = [];
  for c in 0..cc {
    states = states.append(read_lms(input, at + 8 + c * 16));
  }
  let pos = at + 8 + 16 * cc;
  let base = 0;
  while (base < h.frames) {
    let n = clamp(h.frames - base, 0, 20);
    for c in 0..cc {
      let slice = wire.be(input, pos, 8);
      let sf = (slice >> 60) as i32;
      slice = slice << 4;
      let state = states[c];
      for i in 0..n {
        let q = (slice >> 61) as i32;
        let residual = DEQUANT[sf * 8 + q];
        let value = clamp(predict(state) + residual, -32768, 32767);
        samples = samples.with(output_at + (base + i) * cc + c, value);
        state = update(state, value, residual);
        slice = slice << 3;
      }
      states = states.with(c, state);
      pos += 8;
    }
    base += n;
  }
  return samples;
}

pub function decode_frame_into(input: [u8], at: i32, own samples: i32[], limits: pcm.Limits): Result[Frame, pcm.AudioError] {
  let h = frame_header(input, at, limits)?;
  if (samples.len() < h.frames * h.format.channels) {
    return Err(pcm.Buffer);
  }
  return Ok(Frame { format: h.format, frames: h.frames, bytes_read: h.size, samples: decode_samples(input, at, h, samples, 0) });
}

pub function decode_frame(input: [u8], at: i32, limits: pcm.Limits): Result[Frame, pcm.AudioError] {
  let h = frame_header(input, at, limits)?;
  return decode_frame_into(input, at, pcm.zeros(h.frames * h.format.channels), limits);
}

pub function decode(input: [u8], limits: pcm.Limits): Result[pcm.Block, pcm.AudioError] {
  let h = inspect(input, limits)?;
  if (h.frames > 2147483647u32) {
    return Err(pcm.Limit);
  }
  let count = pcm.sample_count(h.frames as i32, h.format.channels, limits)?;
  // Verify the frame chain before the output allocation.
  let at = 8;
  let frames = 0;
  while (frames < h.frames as i32) {
    let f = frame_header(input, at, limits)?;
    if (f.format != h.format || f.frames > h.frames as i32 - frames) {
      return Err(pcm.Header);
    }
    frames += f.frames;
    if (frames < h.frames as i32 && f.frames != FRAME_LEN) {
      return Err(pcm.Header);
    }
    at += f.size;
  }
  if (at != input.len()) {
    return Err(pcm.Header);
  }
  let output = pcm.zeros(count);
  at = 8;
  let offset = 0;
  while (offset < count) {
    let f = frame_header(input, at, limits)?;
    output = decode_samples(input, at, f, output, offset);
    offset += f.frames * h.format.channels;
    at += f.size;
  }
  return Ok(pcm.Block { format: h.format, samples: output });
}

pub function encoder(format: pcm.Format): Result[Encoder, pcm.AudioError] {
  if (!pcm.valid(format) || format.bits_per_sample != 16) {
    return Err(pcm.Unsupported);
  }
  let state: Lms[] = [];
  for _ in 0..format.channels {
    state = state.append(Lms { h0: 0, h1: 0, h2: 0, h3: 0, w0: 0, w1: 0, w2: -8192, w3: 16384 });
  }
  return Ok(Encoder { format: format, state: state });
}

function write_lms(own out: u8[], s: Lms): u8[] {
  out = wire.put_be(out, s.h0 as u64, 2);
  out = wire.put_be(out, s.h1 as u64, 2);
  out = wire.put_be(out, s.h2 as u64, 2);
  out = wire.put_be(out, s.h3 as u64, 2);
  out = wire.put_be(out, s.w0 as u64, 2);
  out = wire.put_be(out, s.w1 as u64, 2);
  out = wire.put_be(out, s.w2 as u64, 2);
  return wire.put_be(out, s.w3 as u64, 2);
}

function scaled(value: i32, sf: i32): i32 {
  let n = value * RECIPROCAL[sf] + 32768 >> 16;
  let sign_v = if (value > 0) { 1 } else if (value < 0) { -1 } else { 0 };
  let sign_n = if (n > 0) { 1 } else if (n < 0) { -1 } else { 0 };
  return n + sign_v - sign_n;
}

struct Slice { bits: u64, state: Lms, sf: i32 }

function encode_slice(samples: [i32], start: i32, n: i32, cc: i32, original: Lms, previous: i32): Slice {
  let best_rank = 18446744073709551615u64;
  let best = Slice { bits: 0u64, state: original, sf: 0 };
  for trial in 0..16 {
    let sf = trial + previous & 15;
    let state = original;
    let bits = sf as u64;
    let rank = 0u64;
    for i in 0..n {
      let sample = samples[start + i * cc];
      let predicted = predict(state);
      let q = QUANT[clamp(scaled(sample - predicted, sf), -8, 8) + 8];
      let residual = DEQUANT[sf * 8 + q];
      let value = clamp(predicted + residual, -32768, 32767);
      let penalty = (state.w0 * state.w0 + state.w1 * state.w1 + state.w2 * state.w2 + state.w3 * state.w3 >> 18) - 2303;
      if (penalty < 0) {
        penalty = 0;
      }
      let error = (sample - value) as i64;
      rank += (error * error) as u64 + (penalty * penalty) as u64;
      if (rank > best_rank) {
        break;
      }
      state = update(state, value, residual);
      bits = bits << 3 | q as u64;
    }
    if (rank < best_rank) {
      best_rank = rank;
      best = Slice { bits: bits, state: state, sf: sf };
    }
  }
  return Slice { ...best, bits: best.bits << ((20 - n) * 3) as u64 };
}

pub function encode_frame(own enc: Encoder, samples: [i32], limits: pcm.Limits): Result[EncodedFrame, pcm.AudioError] {
  let frames = pcm.validate(samples, enc.format, limits)?;
  if (enc.format.bits_per_sample != 16 || frames < 1 || frames > FRAME_LEN || enc.state.len() != enc.format.channels) {
    return Err(pcm.Header);
  }
  let cc = enc.format.channels;
  let size = 8 + 16 * cc + 8 * ((frames + 19) / 20) * cc;
  if (limits.max_input_bytes < size) {
    return Err(pcm.Limit);
  }
  let out: u8[] = [cc as u8];
  out = wire.put_be(out, enc.format.sample_rate as u64, 3);
  out = wire.put_be(out, frames as u64, 2);
  out = wire.put_be(out, size as u64, 2);
  for c in 0..cc {
    out = write_lms(out, enc.state[c]);
  }
  let previous = pcm.zeros(cc);
  let states = enc.state;
  let base = 0;
  while (base < frames) {
    let n = clamp(frames - base, 0, 20);
    for c in 0..cc {
      let slice = encode_slice(samples, base * cc + c, n, cc, states[c], previous[c]);
      states = states.with(c, slice.state);
      previous = previous.with(c, slice.sf);
      out = wire.put_be(out, slice.bits, 8);
    }
    base += n;
  }
  return Ok(EncodedFrame { encoder: Encoder { format: enc.format, state: states }, bytes: out });
}

pub function encode(samples: [i32], format: pcm.Format, limits: pcm.Limits): Result[u8[], pcm.AudioError] {
  let frames = pcm.validate(samples, format, limits)?;
  if (frames < 1) {
    return Err(pcm.Header);
  }
  let enc = encoder(format)?;
  let size = 8i64 + (frames as i64 + 5119i64) / 5120i64 * ((8 + 16 * format.channels) as i64)
    + (frames as i64 + 19i64) / 20i64 * ((8 * format.channels) as i64);
  if (limits.max_input_bytes < 0 || size > limits.max_input_bytes as i64) {
    return Err(pcm.Limit);
  }
  let out: u8[] = [113, 111, 97, 102];
  out = wire.put_be(out, frames as u64, 4);
  let base = 0;
  while (base < frames) {
    let n = clamp(frames - base, 0, FRAME_LEN);
    let frame = encode_frame(enc, samples[base * format.channels:(base + n) * format.channels], limits)?;
    enc = frame.encoder;
    for b in frame.bytes {
      out = out.append(b);
    }
    base += n;
  }
  return Ok(out);
}
```
