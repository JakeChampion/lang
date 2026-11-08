# Bounded ALAC bit input

This module reads most-significant-bit-first fields with checked positions.
Positions retain no input bytes. Incomplete fields return `Truncated`.

```fern
<<*>>=
// Read checked most-significant-bit-first ALAC fields.

import "std/audio/pcm";

pub function read(input: [u8], at: i64, count: i32): Result[(i64, u32), pcm.AudioError] {
  if (at < 0i64 || at > input.len() as i64 * 8i64 || count < 0 || count > 32) {
    return Err(pcm.Header);
  }
  let end = at + count as i64;
  if (end > input.len() as i64 * 8i64) {
    return Err(pcm.Truncated);
  }
  let result = 0u32;
  let left = count;
  while (left > 0) {
    let bit = (at % 8i64) as i32;
    let take = if (left < 8 - bit) { left } else { 8 - bit };
    let value = input[(at / 8i64) as i32] as u32 >> (8 - bit - take) as u32 & (1u32 << take as u32) - 1u32;
    result = result << take as u32 | value;
    at += take as i64;
    left -= take;
  }
  return Ok((end, result));
}

pub function signed(input: [u8], at: i64, count: i32): Result[(i64, i32), pcm.AudioError] {
  if (count < 1 || count > 32) {
    return Err(pcm.Header);
  }
  let (next, value) = read(input, at, count)?;
  let shift = 32 - count;
  return Ok((next, (value << shift as u32) as i32 >> shift));
}

pub function skip(input: [u8], at: i64, count: i64): Result[i64, pcm.AudioError] {
  if (at < 0i64 || count < 0i64) {
    return Err(pcm.Header);
  }
  let Some(end) = at +? count else {
    return Err(pcm.Limit);
  };
  if (end > input.len() as i64 * 8i64) {
    return Err(pcm.Truncated);
  }
  return Ok(end);
}
```
