# WavPack lossless entropy

This module restores lossless residuals from adaptive median codes.
Each block supplies its initial medians.
The output array contains one or two interleaved channels.
The module reuses a caller output array and checks its capacity before decode.

```fern
<<*>>=
// Derived from WavPack 5.8.1, read_words.c and entropy_utils.c.
// Copyright (c) 1998 - 2025 David Bryant.
// Copyright (c) 1998 - 2013 Conifer Software.
// BSD-3-Clause. See licenses/wavpack/BSD-3-Clause.txt.
import "std/audio/pcm";
import "std/audio/wire";
import "std/audio/wavpack/bits";
import "std/audio/wavpack/tables";

pub function expand(log: i32): i32 {
  let magnitude = if (log < 0) { -log } else { log };
  let value = tables.EXP[magnitude & 255] | 256u32;
  let exponent = magnitude >> 8;
  let decoded = if (exponent <= 9) { value >> (9 - exponent) as u32 } else { value << (exponent - 9 & 31) as u32 };
  return (if (log < 0) { 0u32 - decoded } else { decoded }) as i32;
}

pub function medians(input: [u8], at: i32, size: i32, channels: i32): Result[u32[], pcm.AudioError] {
  if (channels != 1 && channels != 2 || size != channels * 6 || at < 0 || at > input.len() || size > input.len() - at) {
    return Err(pcm.Header);
  }
  let out: u32[] = [];
  for i in 0..6 {
    let log = if (i < channels * 3) { wire.le(input, at + i * 2, 2) as i32 } else { 0 };
    if (log > 8447) {
      return Err(pcm.Header);
    }
    out = out.append(expand(log) as u32);
  }
  return Ok(out);
}

function region(own levels: u32[], base: i32, quotient: u32): (u32[], u32, u32) {
  let low = 0u32;
  let high = 0u32;
  if (quotient == 0u32) {
    high = levels[base] >> 4u32;
    levels = levels.with(base, levels[base] - (levels[base] + 126u32) / 128u32 * 2u32);
  } else {
    low = (levels[base] >> 4u32) + 1u32;
    levels = levels.with(base, levels[base] + (levels[base] + 128u32) / 128u32 * 5u32);
    if (quotient == 1u32) {
      high = low + (levels[base + 1] >> 4u32);
      levels = levels.with(base + 1, levels[base + 1] - (levels[base + 1] + 62u32) / 64u32 * 2u32);
    } else {
      low += (levels[base + 1] >> 4u32) + 1u32;
      levels = levels.with(base + 1, levels[base + 1] + (levels[base + 1] + 64u32) / 64u32 * 5u32);
      if (quotient == 2u32) {
        high = low + (levels[base + 2] >> 4u32);
        levels = levels.with(base + 2, levels[base + 2] - (levels[base + 2] + 30u32) / 32u32 * 2u32);
      } else {
        low += (quotient - 2u32) * ((levels[base + 2] >> 4u32) + 1u32);
        high = low + (levels[base + 2] >> 4u32);
        levels = levels.with(base + 2, levels[base + 2] + (levels[base + 2] + 32u32) / 32u32 * 5u32);
      }
    }
  }
  return (levels, low, high);
}

function validate(channels: i32, count: i32, levels: [u32], output: [i32]): Result[void, pcm.AudioError] {
  if (channels != 1 && channels != 2 || levels.len() != 6 || count < 0 || count > output.len()) {
    return Err(pcm.Buffer);
  }
  if (count % channels != 0) {
    return Err(pcm.Buffer);
  }
  return Ok(());
}

function sparse(levels: [u32], holding_zero: boolean, holding_one: u32): boolean {
  return levels[0] < 2u32 && levels[3] < 2u32 && !holding_zero && holding_one == 0u32;
}

function clear_levels(own levels: u32[]): u32[] {
  for j in 0..6 {
    levels = levels.with(j, 0u32);
  }
  return levels;
}

function zero_run(input: [u8], at: i64, end: i64, previous: u32, remaining: i32): Result[(i64, u32), pcm.AudioError] {
  if (previous != 0u32) {
    return Ok((at, previous - 1u32));
  }
  let (next, run) = bits.exponential(input, at, end)?;
  if (run > remaining as u32) {
    return Err(pcm.Header);
  }
  return Ok((next, run));
}

function read_unary(input: [u8], at: i64, end: i64): Result[(i64, u32), pcm.AudioError] {
  let (next, unary) = bits.unary(input, at, end, 16)?;
  if (unary != 16u32) {
    return Ok((next, unary));
  }
  let (stop, extended) = bits.exponential(input, next, end)?;
  if (extended > 4294967279u32) {
    return Err(pcm.Header);
  }
  return Ok((stop, unary + extended));
}

pub function decode(input: [u8], at: i64, end: i64, channels: i32, count: i32, own levels: u32[], own output: i32[]): Result[i32[], pcm.AudioError] {
  validate(channels, count, levels, output)?;
  let holding_zero = false;
  let holding_one = 0u32;
  let zeros = 0u32;
  for i in 0..count {
    if (sparse(levels, holding_zero, holding_one)) {
      let (next, run) = zero_run(input, at, end, zeros, count - i)?;
      at = next;
      zeros = run;
      if (zeros != 0u32) {
        levels = clear_levels(levels);
        output = output.with(i, 0);
        continue;
      }
    }
    let quotient = 0u32;
    if (holding_zero) {
      holding_zero = false;
    } else {
      let (next, coded) = read_unary(input, at, end)?;
      at = next;
      quotient = (coded >> 1u32) + holding_one;
      holding_one = coded & 1u32;
      holding_zero = (coded & 1u32) == 0u32;
    }
    let base = i % channels * 3;
    let (updated, low, high) = region(levels, base, quotient);
    levels = updated;
    let (stop, offset) = bits.code(input, at, end, high - low)?;
    let (next, sign) = bits.read(input, stop, end, 1)?;
    at = next;
    let value = low + offset;
    output = output.with(i, (value ^ 0u32 - sign) as i32);
  }
  return Ok(output);
}
```
