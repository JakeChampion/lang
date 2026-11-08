# Bounded linear audio samples

This module reads integer, floating-point, and G.711 samples from validated byte ranges.
An index retains scalar metadata and no input or sample buffer.
Frame ranges permit exact seeks without earlier samples.
The caller must retain the same input bytes after index creation.

Integer reads preserve the declared signed depth and reject floating-point input.
Float reads preserve finite float values and normalize integer samples to their signed full scale.
They perform no clipping or integer quantization.
Caller-owned output buffers retain every sample after the valid prefix.

```fern
<<*>>=
// Decode bounded linear sample ranges into reusable output storage.

import "core/cmp";
import "std/audio/pcm";
import "std/audio/wire";
import "std/f64";

@derive(cmp.Eq)
pub enum Kind { Signed, Unsigned, Float, MuLaw, ALaw }

pub struct Format { sample_rate: i32, channels: i32, bits: i32, storage_bits: i32, little_endian: boolean, kind: Kind }

pub struct Info { format: Format, frames: i32 }

pub opaque struct Index { info: Info, offset: i32, bytes: i32, input_bytes: i32 }

pub struct Integers { format: pcm.Format, frames: i32, samples: i32[] }

pub struct Floats { sample_rate: i32, channels: i32, frames: i32, samples: f64[] }

// Validate the fields of each scalar sample representation together.
// fern-lint: allow cyclomatic-complexity
function valid(format: Format): boolean {
  if (format.sample_rate < 1 || format.sample_rate > 16777215 || format.channels < 1 || format.channels > 8) {
    return false;
  }
  if (format.kind == Float) {
    return (format.bits == 32 || format.bits == 64) && format.storage_bits == format.bits;
  }
  if (format.kind == MuLaw || format.kind == ALaw) {
    return format.bits == 16 && format.storage_bits == 8;
  }
  return format.bits >= 1 && format.bits <= 32 && format.storage_bits >= format.bits
    && (format.storage_bits == 8 || format.storage_bits == 16 || format.storage_bits == 24 || format.storage_bits == 32);
}

pub function create(input: [u8], offset: i32, bytes: i32, format: Format, limits: pcm.Limits): Result[Index, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (!valid(format) || offset < 0 || offset > input.len() || bytes < 0 || bytes > input.len() - offset) {
    return Err(pcm.Header);
  }
  let stride = format.channels * (format.storage_bits / 8);
  if (bytes % stride != 0) {
    return Err(pcm.Header);
  }
  return Ok(Index { info: Info { format: format, frames: bytes / stride }, offset: offset, bytes: bytes, input_bytes: input.len() });
}

pub function (index: Index) information(): Info {
  return index.info;
}

function range(input: [u8], index: Index, start: i32, frames: i32, capacity: i32, limits: pcm.Limits): Result[(i32, i32), pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (input.len() != index.input_bytes || start < 0 || start > index.info.frames || frames < 0 || frames > index.info.frames - start) {
    return Err(pcm.Header);
  }
  let count = pcm.sample_count(frames, index.info.format.channels, limits)?;
  if (count > capacity) {
    return Err(pcm.Buffer);
  }
  return Ok((index.offset + start * index.info.format.channels * (index.info.format.storage_bits / 8), count));
}

function word(input: [u8], at: i32, format: Format): u64 {
  if (format.little_endian) {
    return wire.le(input, at, format.storage_bits / 8);
  }
  return wire.be(input, at, format.storage_bits / 8);
}

function mulaw(value: u8): i32 {
  let raw = (value ^ 255u8) as i32;
  let magnitude = ((raw & 15) << 3) + 132 << (raw >> 4 & 7);
  return if ((raw & 128) != 0) { 132 - magnitude } else { magnitude - 132 };
}

function alaw(value: u8): i32 {
  let raw = (value ^ 85u8) as i32;
  let segment = raw >> 4 & 7;
  let magnitude = (raw & 15) << 4;
  magnitude += if (segment == 0) { 8 } else { 264 };
  if (segment > 1) {
    magnitude <<= segment - 1;
  }
  return if ((raw & 128) != 0) { magnitude } else { -magnitude };
}

function integer(input: [u8], at: i32, format: Format): i32 {
  if (format.kind == MuLaw) {
    return mulaw(input[at]);
  }
  if (format.kind == ALaw) {
    return alaw(input[at]);
  }
  let encoded = word(input, at, format);
  let raw = encoded as i32;
  let shift = 32 - format.storage_bits;
  let sample = raw << shift >> shift >> format.storage_bits - format.bits;
  if (format.kind == Unsigned) {
    return ((encoded >> (format.storage_bits - format.bits) as u64) as i64 - (1i64 << (format.bits - 1) as i64)) as i32;
  }
  return sample;
}

pub function (index: Index) read_integer_into(input: [u8], start: i32, frames: i32, own output: i32[], limits: pcm.Limits): Result[Integers, pcm.AudioError] {
  let (at, count) = range(input, index, start, frames, output.len(), limits)?;
  let format = index.info.format;
  if (format.kind == Float) {
    return Err(pcm.Unsupported);
  }
  let stride = format.storage_bits / 8;
  for i in 0..count {
    output = output.with(i, integer(input, at + i * stride, format));
  }
  return Ok(Integers { format: pcm.Format { sample_rate: format.sample_rate, channels: format.channels, bits_per_sample: format.bits }, frames: frames, samples: output });
}

pub function (index: Index) read_float_into(input: [u8], start: i32, frames: i32, own output: f64[], limits: pcm.Limits): Result[Floats, pcm.AudioError] {
  let (at, count) = range(input, index, start, frames, output.len(), limits)?;
  let format = index.info.format;
  let stride = format.storage_bits / 8;
  let scale = if (format.kind == Float) { 1f64 } else { (1i64 << (format.bits - 1) as i64) as f64 };
  for i in 0..count {
    let value: f64 = 0f64;
    if (format.kind == Float) {
      let raw = word(input, at + i * stride, format);
      value = if (format.bits == 32) { f32_from_bits(raw as i32) as f64 } else { f64_from_bits(raw as i64) };
      if (!value.is_finite()) {
        return Err(pcm.Sample);
      }
    } else {
      value = integer(input, at + i * stride, format) as f64 / scale;
    }
    output = output.with(i, value);
  }
  return Ok(Floats { sample_rate: format.sample_rate, channels: format.channels, frames: frames, samples: output });
}

pub function (index: Index) read_integer(input: [u8], start: i32, frames: i32, limits: pcm.Limits): Result[pcm.Block, pcm.AudioError] {
  let (_, count) = range(input, index, start, frames, limits.max_samples, limits)?;
  if (index.info.format.kind == Float) {
    return Err(pcm.Unsupported);
  }
  let out = index.read_integer_into(input, start, frames, pcm.zeros(count), limits)?;
  return Ok(pcm.Block { format: out.format, samples: out.samples });
}

pub function (index: Index) read_float(input: [u8], start: i32, frames: i32, limits: pcm.Limits): Result[Floats, pcm.AudioError] {
  let (_, count) = range(input, index, start, frames, limits.max_samples, limits)?;
  let output: f64[] = [];
  for _ in 0..count {
    output = output.append(0f64);
  }
  return index.read_float_into(input, start, frames, output, limits);
}
```
