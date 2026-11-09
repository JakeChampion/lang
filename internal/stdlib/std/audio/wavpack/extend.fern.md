# WavPack sample extensions

This module restores integer low bits and exact IEEE 754 float bit patterns.
It checks the extension checksum when an extension stream is present.
Float restoration uses integer operations and preserves signed zero and NaN payloads.

```fern
<<*>>=
// Derived from WavPack 5.8.1, unpack.c and unpack_floats.c.
// Copyright (c) 1998 - 2025 David Bryant.
// Copyright (c) 1998 - 2013 Conifer Software.
// BSD-3-Clause. See licenses/wavpack/BSD-3-Clause.txt.
import "std/audio/pcm";
import "std/audio/wavpack/bits";

pub struct FloatInfo { flags: u32, shift: i32, exponent: i32, norm: i32, min_zeros: i32, max_ones: i32 }

pub struct IntInfo { sent: i32, zeros: i32, ones: i32, dups: i32, width: i32, shift: i32 }

function zero(input: [u8], at: i64, end: i64, info: FloatInfo): Result[(i64, u32), pcm.AudioError] {
  if ((info.flags & 8u32) == 0u32) {
    return Ok((at, 0u32));
  }
  let (next, sent) = bits.read(input, at, end, 1)?;
  at = next;
  let value = 0u32;
  if (sent != 0u32) {
    let (stop, mantissa) = bits.read(input, at, end, 23)?;
    at = stop;
    value = mantissa;
    if (info.exponent >= 25) {
      let (after_exp, exponent) = bits.read(input, at, end, 8)?;
      at = after_exp;
      value |= exponent << 23u32;
    }
  }
  if (sent != 0u32 || (info.flags & 16u32) != 0u32) {
    let (stop, sign) = bits.read(input, at, end, 1)?;
    at = stop;
    value |= sign << 31u32;
  }
  return Ok((at, value));
}

function shift_zeros(count: i32, info: FloatInfo): i32 {
  let zeros = if (info.max_ones > 0 && count > info.max_ones) { count - info.max_ones } else { 0 };
  if (info.min_zeros > zeros) {
    zeros = if (info.min_zeros > count) { count } else { info.min_zeros };
  }
  return zeros;
}

function shifted(input: [u8], at: i64, end: i64, value: u32, count: i32, info: FloatInfo): Result[(i64, u32), pcm.AudioError] {
  if (count == 0) {
    return Ok((at, value));
  }
  let ones = (info.flags & 1u32) != 0u32;
  if (!ones && (info.flags & 2u32) != 0u32) {
    let (next, same) = bits.read(input, at, end, 1)?;
    at = next;
    ones = same != 0u32;
  }
  let mask = (1u32 << count as u32) - 1u32;
  if (ones) {
    return Ok((at, value | mask));
  }
  if ((info.flags & 4u32) == 0u32) {
    return Ok((at, value));
  }
  let zeros = shift_zeros(count, info);
  let (next, suffix) = bits.read(input, at, end, count - zeros)?;
  return Ok((next, value | suffix << zeros as u32 & mask));
}

function exceptional(input: [u8], at: i64, end: i64, sign: u32): Result[(i64, u32), pcm.AudioError] {
  let (next, sent) = bits.read(input, at, end, 1)?;
  at = next;
  let mantissa = 0u32;
  if (sent != 0u32) {
    let (stop, fraction) = bits.read(input, at, end, 23)?;
    at = stop;
    mantissa = fraction;
  }
  return Ok((at, sign | 2139095040u32 | mantissa));
}

function normalized(value: u32, exponent: i32, extra: boolean): Result[(u32, i32, i32), pcm.AudioError] {
  let count = 0;
  if (value >= 16777216u32) {
    if (extra) {
      return Err(pcm.Sample);
    }
    while ((value & 251658240u32) != 0u32) {
      value >>= 1u32;
      exponent += 1;
    }
  } else {
    while ((value & 8388608u32) == 0u32 && exponent > 1) {
      exponent -= 1;
      count += 1;
      value <<= 1u32;
    }
    if ((value & 8388608u32) == 0u32 && exponent == 1) {
      exponent = 0;
    }
  }
  return Ok((value, exponent, count & 31));
}

function nonzero(input: [u8], at: i64, end: i64, sample: i32, info: FloatInfo, extra: boolean): Result[(i64, u32), pcm.AudioError] {
  let raw = (sample as u32 << info.shift as u32) as i32;
  let sign = if (raw < 0) { 2147483648u32 } else { 0u32 };
  let value = if (raw < 0) { 0u32 - raw as u32 } else { raw as u32 };
  if (extra && value == 16777216u32) {
    return exceptional(input, at, end, sign);
  }
  let (normal, exponent, count) = normalized(value, info.exponent, extra)?;
  let (next, filled) = shifted(input, at, end, normal, count, info)?;
  if (exponent > 255) {
    return Err(pcm.Sample);
  }
  return Ok((next, sign | exponent as u32 << 23u32 | filled & 8388607u32));
}

function validate_float(info: FloatInfo, extra: boolean): Result[void, pcm.AudioError] {
  let shifts = info.shift | info.min_zeros | info.max_ones;
  if (shifts < 0 || shifts > 31 || info.exponent < 0 || info.exponent > 255 || info.flags > 63u32) {
    return Err(pcm.Header);
  }
  if (!extra && (info.flags & 62u32) != 0u32) {
    return Err(pcm.Unsupported);
  }
  return Ok(());
}

pub function floats(input: [u8], at: i64, end: i64, count: i32, info: FloatInfo, extra: boolean, expected: u32, own output: i32[]): Result[i32[], pcm.AudioError] {
  validate_float(info, extra)?;
  if (count < 0 || count > output.len()) {
    return Err(pcm.Buffer);
  }
  let crc = 4294967295u32;
  for i in 0..count {
    let (next, value) = if (output[i] == 0) { zero(input, at, end, info)? } else { nonzero(input, at, end, output[i], info, extra)? };
    at = next;
    crc = crc * 27u32 + (value & 8388607u32) * 9u32 + (value >> 23u32 & 255u32) * 3u32 + (value >> 31u32);
    output = output.with(i, value as i32);
  }
  if (extra && crc != expected) {
    return Err(pcm.Checksum);
  }
  return Ok(output);
}

function validate_integer(info: IntInfo, extra: boolean): Result[void, pcm.AudioError] {
  let combined = info.sent | info.zeros | info.ones | info.dups | info.width | info.shift;
  if (combined < 0 || combined > 31 || info.sent + info.zeros + info.ones + info.dups > 32) {
    return Err(pcm.Header);
  }
  let redundant = (if (info.zeros != 0) { 1 } else { 0 }) + (if (info.ones != 0) { 1 } else { 0 }) + (if (info.dups != 0) { 1 } else { 0 });
  if (redundant > 1 || !extra && info.sent != 0) {
    return Err(pcm.Header);
  }
  return Ok(());
}

function sent_bits(input: [u8], at: i64, end: i64, value: u32, info: IntInfo): Result[(i64, u32), pcm.AudioError] {
  if (info.sent == 0) {
    return Ok((at, value));
  }
  let magnitude = value ^ (value as i32 >> 31) as u32;
  let total = info.sent;
  while (magnitude != 0u32) {
    magnitude >>= 1u32;
    total += 1;
  }
  let read = if (info.width != 0 && total > info.width) { info.sent - (total - info.width) } else { info.sent };
  if (read <= 0) {
    return Ok((at, value << info.sent as u32));
  }
  let (next, suffix) = bits.read(input, at, end, read)?;
  return Ok((next, (value << read as u32 | suffix) << (info.sent - read) as u32));
}

function redundant_bits(value: u32, info: IntInfo): u32 {
  if (info.zeros != 0) {
    return value << info.zeros as u32;
  }
  if (info.ones != 0) {
    return (value + 1u32 << info.ones as u32) - 1u32;
  }
  if (info.dups != 0) {
    let bit = value & 1u32;
    return (value + bit << info.dups as u32) - bit;
  }
  return value;
}

pub function integers(input: [u8], at: i64, end: i64, count: i32, info: IntInfo, extra: boolean, expected: u32, own output: i32[]): Result[i32[], pcm.AudioError] {
  validate_integer(info, extra)?;
  if (count < 0 || count > output.len()) {
    return Err(pcm.Buffer);
  }
  let crc = 4294967295u32;
  for i in 0..count {
    let (next, restored) = sent_bits(input, at, end, output[i] as u32, info)?;
    at = next;
    let value = redundant_bits(restored, info);
    crc = crc * 9u32 + (value & 65535u32) * 3u32 + (value >> 16u32);
    output = output.with(i, (value << info.shift as u32) as i32);
  }
  if (extra && crc != expected) {
    return Err(pcm.Checksum);
  }
  return Ok(output);
}
```
