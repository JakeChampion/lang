# Bounded ALAC bit input

This module reads most-significant-bit-first fields with checked positions.
`window` gives the 64 bits at a position, so one load can supply several fields.
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
  if (count == 0) {
    return Ok((end, 0u32));
  }
  return Ok((end, (window(input, at) >> (64 - count) as u64) as u32));
}

// The 64 bits from bit `at`, most significant first. Bits past the input are zero.
@inline
pub function window(input: [u8], at: i64): u64 {
  let index = (at / 8i64) as i32;
  let bit = (at % 8i64) as u64;
  if (index + 8 <= input.len()) {
    return (input[index] as u64 << 56 | input[index + 1] as u64 << 48 | input[index + 2] as u64 << 40 | input[index + 3] as u64 << 32
      | input[index + 4] as u64 << 24 | input[index + 5] as u64 << 16 | input[index + 6] as u64 << 8 | input[index + 7] as u64) << bit;
  }
  let value = 0u64;
  for i in 0..8 {
    value = value << 8u64 | (if (index + i < input.len()) { input[index + i] as u64 } else { 0u64 });
  }
  return value << bit;
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
