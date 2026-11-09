# CAF ALAC metadata rules

This module validates CAF ALAC metadata and playable sample bounds.
Byte-view and seekable-source readers share these rules.

```fern
<<*>>=
// Share checked CAF ALAC metadata and playable sample bounds across readers.

import "std/audio/alac/config";
import "std/audio/pcm";
import "std/audio/wire";
import "std/f64";

pub struct Description { rate: i32, channels: i32, bytes: i32, frames: i32, depth: i32 }

pub struct Table { count: i32, valid: i64, priming: i64, remainder: i64, declared: boolean }

// Check the complete fixed description before any packet or PCM allocation.
// fern-lint: allow cyclomatic-complexity
pub function description(input: [u8]): Result[Description, pcm.AudioError] {
  if (input.len() != 32) {
    return Err(pcm.Header);
  }
  let at = 0;
  let rate = f64_from_bits(wire.be(input, at, 8) as i64);
  if (!rate.is_finite() || rate < 1.0 || rate > 16777215.0 || (rate as i32) as f64 != rate) {
    return Err(pcm.Header);
  }
  if (!wire.tag(input, at + 8, "alac") || wire.be(input, at + 12, 4) != 0u64) {
    return Err(pcm.Unsupported);
  }
  let bytes = wire.be(input, at + 16, 4);
  let frames = wire.be(input, at + 20, 4);
  let channels = wire.be(input, at + 24, 4);
  let depth = wire.be(input, at + 28, 4);
  if (bytes > 2147483647u64 || frames > 2147483647u64 || depth > 32u64 || channels < 1u64 || channels > 8u64) {
    return Err(pcm.Header);
  }
  return Ok(Description { rate: rate as i32, channels: channels as i32, bytes: bytes as i32, frames: frames as i32, depth: depth as i32 });
}

// Accept raw configuration and the historical frma/alac atom wrappers.
// fern-lint: allow cyclomatic-complexity
pub function cookie(input: [u8]): Result[u8[], pcm.AudioError] {
  let at = 0;
  if (input.len() - at > 72) {
    return Err(pcm.Header);
  }
  if (input.len() - at >= 12 && wire.tag(input, at + 4, "frma")) {
    if (wire.be(input, at, 4) != 12u64 || !wire.tag(input, at + 8, "alac")) {
      return Err(pcm.Header);
    }
    at += 12;
  }
  if (input.len() - at >= 12 && wire.tag(input, at + 4, "alac")) {
    if (wire.be(input, at, 4) != 36u64 || wire.be(input, at + 8, 4) != 0u64) {
      return Err(pcm.Header);
    }
    at += 12;
  }
  if (input.len() - at != 24 && input.len() - at != 48) {
    return Err(pcm.Header);
  }
  let output: u8[] = [];
  for byte in input[at:] {
    output = output.append(byte);
  }
  return Ok(output);
}

pub function variable(input: [u8], at: i32, end: i32): Result[(i32, i32), pcm.AudioError] {
  if (at < 0 || end < at || end > input.len()) {
    return Err(pcm.Header);
  }
  let value = 0u64;
  for _ in 0..5 {
    if (at >= end) {
      return Err(pcm.Truncated);
    }
    let byte = input[at];
    at += 1;
    value = value << 7u64 | (byte & 127u8) as u64;
    if (value > 2147483647u64) {
      return Err(pcm.Limit);
    }
    if ((byte & 128u8) == 0u8) {
      return Ok((at, value as i32));
    }
  }
  return Err(pcm.Header);
}

// Packet counts and signed presentation fields share the same bounded table extent.
// fern-lint: allow cyclomatic-complexity
pub function table(input: [u8], declared: boolean, data_bytes: i64, desc: Description, maximum: i32): Result[Table, pcm.AudioError] {
  if (maximum < 1 || data_bytes < 0i64) {
    return Err(pcm.Limit);
  }
  if (!declared) {
    if (desc.bytes < 1 || data_bytes % (desc.bytes as i64) != 0i64) {
      return Err(pcm.Header);
    }
    let count = data_bytes / (desc.bytes as i64);
    if (count > maximum as i64) {
      return Err(pcm.Limit);
    }
    return Ok(Table { count: count as i32, valid: 0i64, priming: 0i64, remainder: 0i64, declared: false });
  }
  if (input.len() != 24) {
    return Err(pcm.Truncated);
  }
  let count = wire.be(input, 0, 8);
  let valid = wire.be(input, 8, 8);
  let priming = wire.be(input, 16, 4);
  let remainder = wire.be(input, 20, 4);
  if (count > maximum as u64) {
    return Err(pcm.Limit);
  }
  if (valid > 9223372036854775807u64 || priming > 2147483647u64 || remainder > 2147483647u64) {
    return Err(pcm.Header);
  }
  return Ok(Table { count: count as i32, valid: valid as i64, priming: priming as i64, remainder: remainder as i64, declared: true });
}

pub function presentation(value: Table, raw: i64, capacity: i64, last_padding: i64): Result[i64, pcm.AudioError] {
  if (!value.declared) {
    return Ok(raw);
  }
  let Some(end) = value.priming +? value.valid else {
    return Err(pcm.Limit);
  };
  let Some(total) = end +? value.remainder else {
    return Err(pcm.Limit);
  };
  if (total > capacity || value.priming > raw || end > raw && end - raw > last_padding) {
    return Err(pcm.Header);
  }
  return Ok((if (end < raw) { end } else { raw }) - value.priming);
}

pub function check(desc: Description, info: config.Info): Result[void, pcm.AudioError] {
  if (desc.rate != info.format.sample_rate || desc.channels != info.format.channels || desc.depth != 0 && desc.depth != info.format.bits_per_sample || desc.frames != 0 && desc.frames != info.frame_length) {
    return Err(pcm.Header);
  }
  return Ok(());
}
```
