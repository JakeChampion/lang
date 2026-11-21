# CAF linear and G.711 audio

This module indexes version-one CAF files with integer PCM, IEEE float, or G.711 samples.
Integer PCM uses signed 8, 16, 24, or 32-bit samples in either byte order.
Float samples use 32 or 64 bits in either byte order.
The alaw and ulaw tags expand to signed 16-bit PCM.
Multichannel samples retain their stored order.

The returned linear index supports exact frame ranges and reusable integer or float output.
A constant packet table can declare initial and final trim.
The playable index excludes those samples without an extra PCM copy.
Unknown-size final data chunks extend to the input end.

ALAC playback uses the parent CAF module.
Other compression tags, variable packet sizes, and nonintegral sample rates return Unsupported.
The caller must preserve the input bytes after inspection.

```fern
<<*>>=
// Index constant-size CAF samples and apply checked presentation trim.

import "std/audio/caf/chunks";
import "std/audio/linear" as samples;
import "std/audio/pcm";
import "std/audio/wire";
import "std/f64";

pub struct Limits { audio: pcm.Limits, max_chunks: i32 }

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), max_chunks: 65536 };
}

// Validate the scalar format flags, packet stride, and sample width together.
// fern-lint: allow cyclomatic-complexity
function description(input: [u8]): Result[samples.Format, pcm.AudioError] {
  if (input.len() != 32) {
    return Err(pcm.Header);
  }
  let rate = f64_from_bits(wire.be(input, 0, 8) as i64);
  if (!rate.is_finite() || rate < 1f64 || rate > 16777215f64) {
    return Err(pcm.Header);
  }
  if ((rate as i32) as f64 != rate) {
    return Err(pcm.Unsupported);
  }
  let flags = wire.be(input, 12, 4);
  let bytes = wire.be(input, 16, 4);
  let frames = wire.be(input, 20, 4);
  let channels = wire.be(input, 24, 4);
  let bits = wire.be(input, 28, 4);
  if (channels < 1u64 || channels > 8u64) {
    return Err(pcm.Header);
  }
  if (frames != 1u64) {
    return Err(pcm.Unsupported);
  }
  let kind = samples.Signed;
  let depth = bits as i32;
  let storage = depth;
  let little = false;
  if (wire.tag(input, 8, "lpcm")) {
    if (flags > 3u64) {
      return Err(pcm.Unsupported);
    }
    little = (flags & 2u64) != 0u64;
    if ((flags & 1u64) != 0u64) {
      kind = samples.Float;
      if (bits != 32u64 && bits != 64u64) {
        return Err(pcm.Unsupported);
      }
    } else if (bits != 8u64 && bits != 16u64 && bits != 24u64 && bits != 32u64) {
      return Err(pcm.Unsupported);
    }
  } else if (wire.tag(input, 8, "alaw") || wire.tag(input, 8, "ulaw")) {
    if (flags != 0u64 || bits != 8u64) {
      return Err(pcm.Header);
    }
    kind = if (wire.tag(input, 8, "alaw")) { samples.ALaw } else { samples.MuLaw };
    depth = 16;
    storage = 8;
  } else {
    return Err(pcm.Unsupported);
  }
  if (bytes != channels * (storage as u64 / 8u64)) {
    return Err(pcm.Header);
  }
  return Ok(samples.Format { sample_rate: rate as i32, channels: channels as i32, bits: depth, storage_bits: storage, little_endian: little, kind: kind });
}

function presentation(table: [u8], frames: i32): Result[(i32, i32), pcm.AudioError] {
  if (table.len() == 0) {
    return Ok((0, frames));
  }
  if (table.len() != 24) {
    return Err(pcm.Header);
  }
  let packets = wire.be(table, 0, 8);
  let valid = wire.be(table, 8, 8);
  let priming = wire.be(table, 16, 4);
  let remainder = wire.be(table, 20, 4);
  if (packets != frames as u64 || valid > frames as u64 || priming > frames as u64 || remainder > frames as u64) {
    return Err(pcm.Header);
  }
  if (valid + priming + remainder != frames as u64) {
    return Err(pcm.Header);
  }
  return Ok((priming as i32, valid as i32));
}

pub function inspect(input: [u8], limits: Limits): Result[samples.Index, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  let file = chunks.inspect(input, limits.max_chunks)?;
  let table: [u8] = [];
  if let Some(chunk) = file.packets {
    if (chunk.end - chunk.start != 24) {
      return Err(pcm.Header);
    }
    table = input[chunk.start:chunk.end];
  }
  let index = inspect_metadata(input.len(), file.data.start, file.data.end - file.data.start, input[file.description.start:file.description.end], table, limits.audio)?;
  if let Some(chunk) = file.channels {
    check_layout(input[chunk.start:chunk.end], chunk.end - chunk.start, index.information().format.channels)?;
  }
  return Ok(index);
}

// Validate channel counts and record extents. Linear PCM retains stored order,
// so the index does not need to retain speaker labels or coordinates.
pub function check_layout(prefix: [u8], bytes: i32, channels: i32): Result[void, pcm.AudioError] {
  if (prefix.len() < 12 || bytes < 12) { return Err(pcm.Header); }
  let tag = wire.be(prefix, 0, 4);
  let bitmap = wire.be(prefix, 4, 4);
  let descriptions = wire.be(prefix, 8, 4);
  if (12u64 + descriptions * 20u64 != bytes as u64) { return Err(pcm.Header); }
  if (tag == 0u64) {
    if (bitmap != 0u64 || descriptions != channels as u64) { return Err(pcm.Header); }
  } else if (tag == 65536u64) {
    let count = 0;
    for i in 0..32 { count += ((bitmap >> i as u64) & 1u64) as i32; }
    if (descriptions != 0u64 || count != channels) { return Err(pcm.Header); }
  } else if (bitmap != 0u64 || descriptions != 0u64 || (tag & 65535u64) != channels as u64) {
    return Err(pcm.Header);
  }
  return Ok(());
}

// An absent packet table is represented by an empty view.
pub function inspect_metadata(file_bytes: i32, offset: i32, bytes: i32, desc: [u8], table: [u8], limits: pcm.Limits): Result[samples.Index, pcm.AudioError] {
  let format = description(desc)?;
  let physical = samples.create_extent(file_bytes, offset, bytes, format, limits)?;
  let (skip, frames) = presentation(table, physical.information().frames)?;
  let stride = format.channels * (format.storage_bits / 8);
  return samples.create_extent(file_bytes, offset + skip * stride, frames * stride, format, limits);
}

pub function decode(input: [u8], limits: Limits): Result[pcm.Block, pcm.AudioError] {
  let index = inspect(input, limits)?;
  return index.read_integer(input, 0, index.information().frames, limits.audio);
}

pub function decode_float(input: [u8], limits: Limits): Result[samples.Floats, pcm.AudioError] {
  let index = inspect(input, limits)?;
  return index.read_float(input, 0, index.information().frames, limits.audio);
}
```
