# RIFF WAVE integer and float PCM

The decoder supports integer PCM at 8, 16, 24, and 32 bits, including WAVE_FORMAT_EXTENSIBLE PCM.
Eight-bit WAVE samples are unsigned on disk. The API converts them to signed PCM.

`inspect_linear` also accepts IEEE float at 32 or 64 bits, including its extensible subformat.
Its linear index supports exact frame ranges and reusable integer or float output.
Float reads preserve finite values without clipping. Integer reads reject float input.

`inspect` validates chunk bounds and format fields without sample allocation.
`decode_range` reads a frame range. `decode` reads the complete data chunk.

The encoder writes PCM WAVE with standard channel masks for multichannel audio.
Custom channel masks return `Unsupported`. Compressed WAVE, RIFX, and RF64 return `Unsupported`.

```fern
<<*>>=
// Read bounded integer and float WAVE samples and write integer PCM files.

import "std/audio/pcm";
import "std/audio/linear";
import "std/array";
import "std/audio/wire";

pub struct Header { format: pcm.Format, frames: i32, data_offset: i32, data_bytes: i32 }

const CHANNEL_MASK: u64[] = [0u64, 4u64, 3u64, 7u64, 51u64, 55u64, 63u64, 1807u64, 1599u64];

// fern-lint: allow cyclomatic-complexity
// One check per field of the format chunk and its extensible form.
function parse_format(input: [u8], at: i32, size: i32): Result[linear.Format, pcm.AudioError] {
  if (size < 16) {
    return Err(pcm.Header);
  }
  let kind = wire.le(input, at, 2) as i32;
  let channels = wire.le(input, at + 2, 2) as i32;
  if (channels < 1 || channels > 8) {
    return Err(pcm.Header);
  }
  let rate = wire.le(input, at + 4, 4);
  let byte_rate = wire.le(input, at + 8, 4);
  let align = wire.le(input, at + 12, 2) as i32;
  let bits = wire.le(input, at + 14, 2) as i32;
  if (kind == 65534) {
    if (size < 40 || wire.le(input, at + 16, 2) < 22u64
      || wire.le(input, at + 16, 2) > (size - 18) as u64) {
      return Err(pcm.Header);
    }
    let valid_bits = wire.le(input, at + 18, 2) as i32;
    if (valid_bits != bits) {
      return Err(pcm.Unsupported);
    }
    let guid: u8[] = [1, 0, 0, 0, 0, 0, 16, 0, 128, 0, 0, 170, 0, 56, 155, 113];
    kind = wire.le(input, at + 24, 4) as i32;
    for i in 4..16 {
      if (input[at + 24 + i] != guid[i]) {
        return Err(pcm.Unsupported);
      }
    }
    let mask = wire.le(input, at + 20, 4);
    if (mask != 0u64) {
      let count = 0;
      for i in 0..32 {
        if ((mask >> i as u64 & 1u64) != 0u64) {
          count += 1;
        }
      }
      if (count != channels) {
        return Err(pcm.Header);
      }
      if (mask != CHANNEL_MASK[channels]) {
        return Err(pcm.Unsupported);
      }
    }
  }
  if (kind != 1 && kind != 3) {
    return Err(pcm.Unsupported);
  }
  if (kind == 1 && bits != 8 && bits != 16 && bits != 24 && bits != 32
    || kind == 3 && bits != 32 && bits != 64) {
    return Err(pcm.Unsupported);
  }
  if (rate < 1u64 || rate > 16777215u64) {
    return Err(pcm.Header);
  }
  if (align != channels * (bits / 8)
    || byte_rate != rate * (align as u64)) {
    return Err(pcm.Header);
  }
  let encoding = if (kind == 3) { linear.Float } else if (bits == 8) { linear.Unsigned } else { linear.Signed };
  return Ok(linear.Format { sample_rate: rate as i32, channels: channels, bits: bits, storage_bits: bits, little_endian: true, kind: encoding });
}

// fern-lint: allow cyclomatic-complexity
// One arm per RIFF chunk kind, and one check per header field.
pub function inspect_linear(input: [u8], limits: pcm.Limits): Result[linear.Index, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (input.len() < 12) {
    return Err(pcm.Truncated);
  }
  if (wire.tag(input, 0, "RF64") || wire.tag(input, 0, "RIFX")) {
    return Err(pcm.Unsupported);
  }
  if (!wire.tag(input, 0, "RIFF") || !wire.tag(input, 8, "WAVE")) {
    return Err(pcm.Header);
  }
  let declared = wire.le(input, 4, 4) + 8u64;
  if (declared > input.len() as u64) {
    return Err(pcm.Truncated);
  }
  if (declared != input.len() as u64) {
    return Err(pcm.Header);
  }
  let at = 12;
  let format = linear.Format { sample_rate: 0, channels: 0, bits: 0, storage_bits: 0, little_endian: true, kind: linear.Signed };
  let have_format = false;
  let data_at = -1;
  let data_bytes = 0;
  while (at < input.len()) {
    if (input.len() - at < 8) {
      return Err(pcm.Truncated);
    }
    let size64 = wire.le(input, at + 4, 4);
    if (size64 > (input.len() - at - 8) as u64) {
      return Err(pcm.Truncated);
    }
    let size = size64 as i32;
    if (wire.tag(input, at, "fmt ")) {
      if (have_format) {
        return Err(pcm.Header);
      }
      format = parse_format(input, at + 8, size)?;
      have_format = true;
    }
    if (wire.tag(input, at, "data")) {
      if (data_at >= 0 || !have_format) {
        return Err(pcm.Header);
      }
      data_at = at + 8;
      data_bytes = size;
    }
    at += 8 + size;
    if (size % 2 != 0) {
      if (at == input.len()) {
        return Err(pcm.Truncated);
      }
      at += 1;
    }
  }
  if (!have_format || data_at < 0) {
    return Err(pcm.Header);
  }
  return linear.create(input, data_at, data_bytes, format, limits);
}

pub function inspect(input: [u8], limits: pcm.Limits): Result[Header, pcm.AudioError] {
  let index = inspect_linear(input, limits)?;
  let info = index.information();
  if (info.format.kind == linear.Float) {
    return Err(pcm.Unsupported);
  }
  let format = pcm.Format { sample_rate: info.format.sample_rate, channels: info.format.channels, bits_per_sample: info.format.bits };
  return Ok(Header { format: format, frames: info.frames, data_offset: index.data_offset(), data_bytes: index.data_bytes() });
}

pub function decode_range(input: [u8], start: i32, frames: i32, limits: pcm.Limits): Result[pcm.Block, pcm.AudioError] {
  let index = inspect_linear(input, limits)?;
  return index.read_integer(input, start, frames, limits);
}

pub function decode(input: [u8], limits: pcm.Limits): Result[pcm.Block, pcm.AudioError] {
  let index = inspect_linear(input, limits)?;
  return index.read_integer(input, 0, index.information().frames, limits);
}

pub function decode_float(input: [u8], limits: pcm.Limits): Result[linear.Floats, pcm.AudioError] {
  let index = inspect_linear(input, limits)?;
  return index.read_float(input, 0, index.information().frames, limits);
}

// fern-lint: allow cyclomatic-complexity
// One header field per write, and the extensible header adds its own.
pub function encode(samples: [i32], format: pcm.Format, limits: pcm.Limits): Result[u8[], pcm.AudioError] {
  let _ = pcm.validate(samples, format, limits)?;
  let bits = format.bits_per_sample;
  if (bits != 8 && bits != 16 && bits != 24 && bits != 32) {
    return Err(pcm.Unsupported);
  }
  let size = samples.len() as i64 * ((bits / 8) as i64);
  let extensible = format.channels > 2;
  let total = (if (extensible) { 68i64 } else { 44i64 }) + size + size % 2i64;
  if (limits.max_input_bytes < 0 || total > limits.max_input_bytes as i64 || total > 2147483647i64) {
    return Err(pcm.Limit);
  }
  let out: u8[] = [82, 73, 70, 70];
  out = wire.put_le(out, (total - 8i64) as u64, 4);
  out = out.concat([87 as u8, 65 as u8, 86 as u8, 69 as u8, 102 as u8, 109 as u8, 116 as u8, 32 as u8]);
  out = wire.put_le(out, if (extensible) { 40u64 } else { 16u64 }, 4);
  out = wire.put_le(out, if (extensible) { 65534u64 } else { 1u64 }, 2);
  out = wire.put_le(out, format.channels as u64, 2);
  out = wire.put_le(out, format.sample_rate as u64, 4);
  let stride = format.channels * (bits / 8);
  out = wire.put_le(out, format.sample_rate as u64 * (stride as u64), 4);
  out = wire.put_le(out, stride as u64, 2);
  out = wire.put_le(out, bits as u64, 2);
  if (extensible) {
    out = wire.put_le(out, 22u64, 2);
    out = wire.put_le(out, bits as u64, 2);
    out = wire.put_le(out, CHANNEL_MASK[format.channels], 4);
    out = out.concat([1 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 0 as u8, 16 as u8, 0 as u8, 128 as u8, 0 as u8, 0 as u8, 170 as u8, 0 as u8, 56 as u8, 155 as u8, 113 as u8]);
  }
  out = out.concat([100 as u8, 97 as u8, 116 as u8, 97 as u8]);
  out = wire.put_le(out, size as u64, 4);
  for s in samples {
    let value = if (bits == 8) { s + 128 } else { s };
    out = wire.put_le(out, value as u64, bits / 8);
  }
  if (size % 2i64 != 0i64) {
    out = out.append(0);
  }
  return Ok(out);
}
```
