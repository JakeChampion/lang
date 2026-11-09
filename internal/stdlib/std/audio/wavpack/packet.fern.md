# WavPack audio packets

This module restores one validated lossless WavPack block.
The output contains one or two interleaved channels.
Float output uses exact IEEE 754 bit patterns in the integer array.
Only the declared sample prefix is valid when the caller reuses a larger array.
Hybrid audio, correction files, and DSD return `Unsupported`.

```fern
<<*>>=
// Restore a complete lossless WavPack block in reusable sample storage.
import "std/audio/pcm";
import "std/audio/wire";
import "std/audio/wavpack/block";
import "std/audio/wavpack/bits";
import "std/audio/wavpack/entropy";
import "std/audio/wavpack/predict";
import "std/audio/wavpack/extend";

pub function find(value: block.Block, id: i32): Option[block.Entry] {
  for entry in value.metadata {
    if (entry.id == id) {
      return Some(entry);
    }
  }
  return None;
}

function entry(value: block.Block, id: i32): block.Entry {
  return match (find(value, id)) { Some(found) => found, None => block.Entry { id: id, at: 0, size: 0 } };
}

function unsupported_metadata(id: i32): boolean {
  return id < 32 && (id > 13 || id == 6 || id == 7 || id == 11);
}

pub function channels(value: block.Block): i32 {
  return if ((value.flags & 4u32) != 0u32) { 1 } else { 2 };
}

pub function validate_metadata(value: block.Block): Result[void, pcm.AudioError] {
  let seen = 0u64;
  for part in value.metadata {
    if (part.id == 0) {
      continue;
    }
    let bit = 1u64 << part.id as u64;
    if ((seen & bit) != 0u64) {
      return Err(pcm.Header);
    }
    seen |= bit;
    if (unsupported_metadata(part.id)) {
      return Err(pcm.Unsupported);
    }
  }
  if ((seen & 1u64 << 12u64) != 0u64 && (seen & 1u64 << 44u64) != 0u64) {
    return Err(pcm.Header);
  }
  return Ok(());
}

function decorrelation(input: [u8], value: block.Block, channels: i32): Result[predict.Pass[], pcm.AudioError] {
  let t = entry(value, 2);
  let w = entry(value, 3);
  let s = entry(value, 4);
  let passes = predict.terms(input[t.at:t.at + t.size], channels)?;
  passes = predict.weights(input[w.at:w.at + w.size], channels, passes)?;
  return predict.samples(input[s.at:s.at + s.size], channels, passes);
}

struct Extension { at: i64, end: i64, present: boolean, expected: u32, width: i32, minimum: i32, maximum: i32 }

function extra_span(input: [u8], x: block.Entry): Result[Extension, pcm.AudioError] {
  if (x.size == 0) {
    return Ok(Extension { at: 0i64, end: 0i64, present: false, expected: 0u32, width: 0, minimum: 0, maximum: 0 });
  }
  if (x.size <= 4 || x.size % 2 != 0) {
    return Err(pcm.Header);
  }
  return Ok(Extension { at: (x.at as i64 + 4i64) * 8i64, end: (x.at as i64 + x.size as i64) * 8i64, present: true, expected: wire.le(input, x.at, 4) as u32, width: 0, minimum: 0, maximum: 0 });
}

function read_extra(input: [u8], value: block.Block): Result[Extension, pcm.AudioError] {
  let x = match (find(value, 44)) { Some(found) => found, None => entry(value, 12) };
  let span = extra_span(input, x)?;
  let floating = (value.flags & 128u32) != 0u32;
  if (span.present && x.id == 44) {
    let (next, field) = bits.read(input, span.at, span.end, 5)?;
    if (floating) {
      let (stop, ones) = bits.read(input, next, span.end, 5)?;
      span = Extension { ...span, at: stop, minimum: field as i32, maximum: ones as i32 };
    } else {
      span = Extension { ...span, at: next, width: field as i32 };
    }
  }
  return Ok(span);
}

function float_extension(input: [u8], value: block.Block, x: Extension, count: i32, own output: i32[]): Result[i32[], pcm.AudioError] {
  let f = entry(value, 8);
  if (f.size != 4 || (value.flags & 259u32) != 3u32 || input[f.at] > 63u8) {
    return Err(pcm.Header);
  }
  let info = extend.FloatInfo { flags: input[f.at] as u32, shift: input[f.at + 1] as i32, exponent: input[f.at + 2] as i32, norm: input[f.at + 3] as i32, min_zeros: x.minimum, max_ones: x.maximum };
  return extend.floats(input, x.at, x.end, count, info, x.present, x.expected, output);
}

function extension(input: [u8], value: block.Block, count: i32, own output: i32[]): Result[i32[], pcm.AudioError] {
  let x = read_extra(input, value)?;
  if ((value.flags & 128u32) != 0u32) {
    return float_extension(input, value, x, count, output);
  }
  let shift = (value.flags >> 13u32 & 31u32) as i32;
  if ((value.flags & 256u32) != 0u32) {
    let integer = entry(value, 9);
    if (integer.size != 4) {
      return Err(pcm.Header);
    }
    let info = extend.IntInfo { sent: input[integer.at] as i32, zeros: input[integer.at + 1] as i32, ones: input[integer.at + 2] as i32, dups: input[integer.at + 3] as i32, width: x.width, shift: shift };
    return extend.integers(input, x.at, x.end, count, info, x.present, x.expected, output);
  }
  if (x.present) {
    return Err(pcm.Header);
  }
  for i in 0..count {
    output = output.with(i, (output[i] as u32 << shift as u32) as i32);
  }
  return Ok(output);
}

function reconstruct(own output: i32[], count: i32, joint: boolean, expected: u32): Result[i32[], pcm.AudioError] {
  let crc = 4294967295u32;
  if (joint) {
    for i in 0..count / 2 {
      let right = output[i * 2 + 1] - (output[i * 2] >> 1);
      let left = output[i * 2] + right;
      output = output.with(i * 2, left).with(i * 2 + 1, right);
    }
  }
  for i in 0..count {
    crc = crc * 3u32 + output[i] as u32;
  }
  if (crc != expected) {
    return Err(pcm.Checksum);
  }
  return Ok(output);
}

function coded_channels(value: block.Block): Result[i32, pcm.AudioError] {
  if ((value.flags & 2147483656u32) != 0u32) {
    return Err(pcm.Unsupported);
  }
  let mono = (value.flags & 1073741828u32) != 0u32;
  if (value.frames == 0 || mono && (value.flags & 16u32) != 0u32 || (value.flags & 1073741828u32) == 1073741828u32) {
    return Err(pcm.Header);
  }
  return Ok(if (mono) { 1 } else { 2 });
}

function residuals(input: [u8], value: block.Block, coded_channels: i32, coded_count: i32, own output: i32[]): Result[i32[], pcm.AudioError] {
  let entropy_info = entry(value, 5);
  let levels = entropy.medians(input, entropy_info.at, entropy_info.size, coded_channels)?;
  let Some(encoded) = find(value, 10) else {
    return Err(pcm.Header);
  };
  let passes = decorrelation(input, value, coded_channels)?;
  output = entropy.decode(input, encoded.at as i64 * 8i64, (encoded.at as i64 + encoded.size as i64) * 8i64, coded_channels, coded_count, levels, output)?;
  return predict.restore(output, value.frames, coded_channels, passes);
}

function duplicate_stereo(own output: i32[], frames: i32): i32[] {
  let i = frames;
  while (i > 0) {
    i -= 1;
    let sample = output[i];
    output = output.with(i * 2, sample).with(i * 2 + 1, sample);
  }
  return output;
}

pub function decode(input: [u8], value: block.Block, limits: pcm.Limits, own output: i32[]): Result[i32[], pcm.AudioError] {
  validate_metadata(value)?;
  let channels = channels(value);
  let count = pcm.sample_count(value.frames, channels, limits)?;
  let coded_channels = coded_channels(value)?;
  let coded_count = value.frames * coded_channels;
  while (output.len() < count) {
    output = output.append(0);
  }
  output = residuals(input, value, coded_channels, coded_count, output)?;
  output = reconstruct(output, coded_count, (value.flags & 16u32) != 0u32, value.crc)?;
  output = extension(input, value, coded_count, output)?;
  if (channels == 2 && coded_channels == 1) {
    output = duplicate_stereo(output, value.frames);
  }
  return Ok(output);
}
```
