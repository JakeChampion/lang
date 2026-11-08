# AIFF and AIFF-C audio

This module indexes complete AIFF and AIFF-C byte views.
Integer PCM supports declared depths from 1 through 32 bits in whole-byte storage.
Unused low bits do not contribute to the signed sample value.
AIFF-C accepts NONE, twos, sowt, IEEE 32-bit and 64-bit float, A-law, and mu-law.
Multichannel samples retain their stored order.

Inspection checks FORM extents, padded chunks, format versions, integral sample rates, and sound offsets.
The returned linear index supports exact frame ranges and reusable output buffers.
Other compression tags and nonzero sound block sizes return Unsupported.
Float reads preserve finite values without clipping or integer quantization.

```fern
<<*>>=
// Index bounded AIFF chunks and their linear sample representation.

import "std/audio/linear";
import "std/audio/pcm";
import "std/audio/wire";

pub struct Limits { audio: pcm.Limits, max_chunks: i32 }

struct Common { format: linear.Format, frames: i32 }

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), max_chunks: 65536 };
}

function sample_rate(input: [u8], at: i32): Result[i32, pcm.AudioError] {
  let exponent = wire.be(input, at, 2);
  let mantissa = wire.be(input, at + 2, 8);
  if (exponent < 16383u64 || exponent > 16406u64 || mantissa >> 63u64 != 1u64) {
    return Err(pcm.Header);
  }
  let shift = 16446u64 - exponent;
  if ((mantissa & (1u64 << shift) - 1u64) != 0u64) {
    return Err(pcm.Unsupported);
  }
  let rate = mantissa >> shift;
  if (rate < 1u64 || rate > 16777215u64) {
    return Err(pcm.Header);
  }
  return Ok(rate as i32);
}

// Each compression tag selects one complete scalar sample representation.
// fern-lint: allow cyclomatic-complexity
function encoding(input: [u8], at: i32): Result[(linear.Kind, i32, boolean), pcm.AudioError] {
  if (wire.tag(input, at, "NONE") || wire.tag(input, at, "twos")) {
    return Ok((linear.Signed, 0, false));
  }
  if (wire.tag(input, at, "sowt")) {
    return Ok((linear.Signed, 0, true));
  }
  if (wire.tag(input, at, "fl32") || wire.tag(input, at, "FL32")) {
    return Ok((linear.Float, 32, false));
  }
  if (wire.tag(input, at, "fl64") || wire.tag(input, at, "FL64")) {
    return Ok((linear.Float, 64, false));
  }
  if (wire.tag(input, at, "ulaw") || wire.tag(input, at, "ULAW")) {
    return Ok((linear.MuLaw, 16, false));
  }
  if (wire.tag(input, at, "alaw") || wire.tag(input, at, "ALAW")) {
    return Ok((linear.ALaw, 16, false));
  }
  return Err(pcm.Unsupported);
}

// Keep the Pascal compression name and its padding inside the common chunk.
// fern-lint: allow cyclomatic-complexity
function common(input: [u8], at: i32, size: i32, compressed: boolean): Result[Common, pcm.AudioError] {
  if (size < 18 || compressed && size < 24) {
    return Err(pcm.Truncated);
  }
  let channels = wire.be(input, at, 2) as i32;
  let frames = wire.be(input, at + 2, 4);
  let bits = wire.be(input, at + 6, 2) as i32;
  let rate = sample_rate(input, at + 8)?;
  if (frames > 2147483647u64) {
    return Err(pcm.Limit);
  }
  let kind = linear.Signed;
  let little = false;
  let depth = bits;
  let storage = (bits + 7) / 8 * 8;
  if (compressed) {
    let name = input[at + 22] as i32;
    if (23 + name + (1 + name) % 2 > size) {
      return Err(pcm.Truncated);
    }
    let (codec, fixed, le) = encoding(input, at + 18)?;
    kind = codec;
    little = le;
    if (fixed != 0) {
      if (bits != fixed && !(fixed == 16 && (kind == linear.MuLaw || kind == linear.ALaw) && bits == 8)) {
        return Err(pcm.Header);
      }
      depth = fixed;
      storage = if (kind == linear.Float) { fixed } else { 8 };
    }
  }
  if (kind == linear.Signed && (bits < 1 || bits > 32)) {
    return Err(pcm.Unsupported);
  }
  return Ok(Common { format: linear.Format { sample_rate: rate, channels: channels, bits: depth, storage_bits: storage, little_endian: little, kind: kind }, frames: frames as i32 });
}

// Chunk extents, singleton records, and sound offsets share one bounded scan.
// fern-lint: allow cyclomatic-complexity
pub function inspect(input: [u8], limits: Limits): Result[linear.Index, pcm.AudioError] {
  pcm.check_input(input, limits.audio)?;
  if (limits.max_chunks < 1) {
    return Err(pcm.Limit);
  }
  if (input.len() < 12) {
    return Err(pcm.Truncated);
  }
  if (!wire.tag(input, 0, "FORM")) {
    return Err(pcm.Header);
  }
  let compressed = wire.tag(input, 8, "AIFC");
  if (!compressed && !wire.tag(input, 8, "AIFF")) {
    return Err(pcm.Unsupported);
  }
  let declared = wire.be(input, 4, 4) + 8u64;
  if (declared != input.len() as u64) {
    return Err(if (declared > input.len() as u64) { pcm.Truncated } else { pcm.Header });
  }
  let at = 12;
  let seen = 0;
  let version = false;
  let desc: Option[Common] = None;
  let sound = -1;
  let sound_bytes = 0;
  while (at < input.len()) {
    seen += 1;
    if (seen > limits.max_chunks) {
      return Err(pcm.Limit);
    }
    if (input.len() - at < 8) {
      return Err(pcm.Truncated);
    }
    let size = wire.be(input, at + 4, 4);
    if (size > (input.len() - at - 8) as u64) {
      return Err(pcm.Truncated);
    }
    let bytes = size as i32;
    let data = at + 8;
    if (wire.tag(input, at, "COMM")) {
      if let Some(_) = desc {
        return Err(pcm.Header);
      }
      desc = Some(common(input, data, bytes, compressed)?);
    } else if (wire.tag(input, at, "FVER")) {
      if (!compressed || version || bytes != 4) {
        return Err(pcm.Header);
      }
      if (wire.be(input, data, 4) != 2726318400u64) {
        return Err(pcm.Unsupported);
      }
      version = true;
    } else if (wire.tag(input, at, "SSND")) {
      if (sound >= 0 || bytes < 8) {
        return Err(pcm.Header);
      }
      let offset = wire.be(input, data, 4);
      if (offset > (bytes - 8) as u64) {
        return Err(pcm.Truncated);
      }
      if (wire.be(input, data + 4, 4) != 0u64) {
        return Err(pcm.Unsupported);
      }
      sound = data + 8 + offset as i32;
      sound_bytes = bytes - 8 - offset as i32;
    }
    at = data + bytes;
    if (bytes % 2 != 0) {
      if (at == input.len()) {
        return Err(pcm.Truncated);
      }
      at += 1;
    }
  }
  let Some(description) = desc else {
    return Err(pcm.Header);
  };
  if (sound < 0 || compressed && !version) {
    return Err(pcm.Header);
  }
  let index = linear.create(input, sound, sound_bytes, description.format, limits.audio)?;
  if (index.information().frames != description.frames) {
    return Err(pcm.Header);
  }
  return Ok(index);
}

pub function decode(input: [u8], limits: Limits): Result[pcm.Block, pcm.AudioError] {
  let index = inspect(input, limits)?;
  return index.read_integer(input, 0, index.information().frames, limits.audio);
}

pub function decode_float(input: [u8], limits: Limits): Result[linear.Floats, pcm.AudioError] {
  let index = inspect(input, limits)?;
  return index.read_float(input, 0, index.information().frames, limits.audio);
}
```
