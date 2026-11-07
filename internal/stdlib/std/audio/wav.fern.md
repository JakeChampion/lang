# RIFF WAVE PCM

The decoder supports integer PCM at 8, 16, 24, and 32 bits, including WAVE_FORMAT_EXTENSIBLE PCM.
Eight-bit WAVE samples are unsigned on disk. The API converts them to signed PCM.

`inspect` validates chunk bounds and format fields without sample allocation.
`decode_range` reads a frame range. `decode` reads the complete data chunk.

The encoder writes PCM WAVE with standard channel masks for multichannel audio.
Custom channel masks return `Unsupported`. Float, compressed WAVE, RIFX, and RF64 return `Unsupported`.

```fern
<<*>>=
import "std/audio/pcm";
import "std/array";
import "std/audio/wire";

pub struct Header { format: pcm.Format, frames: i32, data_offset: i32, data_bytes: i32 }

const CHANNEL_MASK: u64[] = [0u64, 4u64, 3u64, 7u64, 51u64, 55u64, 63u64, 1807u64, 1599u64];

function parse_format(input: [u8], at: i32, size: i32): Result[pcm.Format, pcm.AudioError] {
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
    for i in 0..16 {
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
  } else if (kind != 1) {
    return Err(pcm.Unsupported);
  }
  if (bits != 8 && bits != 16 && bits != 24 && bits != 32) {
    return Err(pcm.Unsupported);
  }
  if (rate > 16777215u64) {
    return Err(pcm.Header);
  }
  let format = pcm.Format { sample_rate: rate as i32, channels: channels, bits_per_sample: bits };
  if (!pcm.valid(format) || align != channels * (bits / 8)
    || byte_rate != rate * (align as u64)) {
    return Err(pcm.Header);
  }
  return Ok(format);
}

pub function inspect(input: [u8], limits: pcm.Limits): Result[Header, pcm.AudioError] {
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
  let format = pcm.Format { sample_rate: 0, channels: 0, bits_per_sample: 0 };
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
  let stride = format.channels * (format.bits_per_sample / 8);
  if (data_bytes % stride != 0) {
    return Err(pcm.Header);
  }
  return Ok(Header { format: format, frames: data_bytes / stride, data_offset: data_at, data_bytes: data_bytes });
}

pub function decode_range(input: [u8], start: i32, frames: i32, limits: pcm.Limits): Result[pcm.Block, pcm.AudioError] {
  let header = inspect(input, limits)?;
  if (start < 0 || start > header.frames || frames < 0 || frames > header.frames - start) {
    return Err(pcm.Header);
  }
  let count = pcm.sample_count(frames, header.format.channels, limits)?;
  let bytes = header.format.bits_per_sample / 8;
  let at = header.data_offset + start * header.format.channels * bytes;
  let samples: i32[] = [];
  for i in 0..count {
    let value = wire.le(input, at + i * bytes, bytes) as i32;
    if (bytes == 1) {
      value -= 128;
    } else if (bytes < 4) {
      let shift = 32 - bytes * 8;
      value = value << shift >> shift;
    }
    samples = samples.append(value);
  }
  return Ok(pcm.Block { format: header.format, samples: samples });
}

pub function decode(input: [u8], limits: pcm.Limits): Result[pcm.Block, pcm.AudioError] {
  let header = inspect(input, limits)?;
  return decode_range(input, 0, header.frames, limits);
}

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
