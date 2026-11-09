# WavPack bit fields

Fields use least significant bit order.
Every read checks the declared bitstream boundary.
The exponential code rejects more than 32 prefix bits.
Unary limits must be nonnegative.

```fern
<<*>>=
// Bounded little-endian bit reads for WavPack entropy and extension streams.
import "std/audio/pcm";
import "std/u32";

function valid_span(input: [u8], at: i64, end: i64): boolean {
  return at >= 0i64 && end >= at && end <= input.len() as i64 * 8i64;
}

pub function read(input: [u8], at: i64, end: i64, count: i32): Result[(i64, u32), pcm.AudioError] {
  if (count < 0 || count > 32 || !valid_span(input, at, end)) {
    return Err(pcm.Header);
  }
  if (count as i64 > end - at) {
    return Err(pcm.Truncated);
  }
  let value = 0u64;
  let shift = (at & 7i64) as i32;
  let bytes = (count + shift + 7) / 8;
  for i in 0..bytes {
    value |= input[(at / 8i64) as i32 + i] as u64 << (i * 8) as u64;
  }
  let mask = if (count == 32) { 4294967295u64 } else { (1u64 << count as u64) - 1u64 };
  return Ok((at + count as i64, (value >> shift as u64 & mask) as u32));
}

function unary_width(at: i64, end: i64, remaining: i64): i32 {
  let width = 8i64 - (at & 7i64);
  if (width > end - at) {
    width = end - at;
  }
  if (width > remaining) {
    width = remaining;
  }
  return width as i32;
}

pub function unary(input: [u8], at: i64, end: i64, limit: i32): Result[(i64, u32), pcm.AudioError] {
  if (limit < 0 || !valid_span(input, at, end)) {
    return Err(pcm.Header);
  }
  let count = 0u32;
  while (count <= limit as u32) {
    let width = unary_width(at, end, limit as i64 - count as i64 + 1i64);
    if (width == 0) {
      return Err(pcm.Truncated);
    }
    let (next, value) = read(input, at, end, width)?;
    let ones = (~value).trailing_zeros();
    if (ones < width) {
      return Ok((at + ones as i64 + 1i64, count + ones as u32));
    }
    at = next;
    count += width as u32;
  }
  return Err(pcm.Header);
}

pub function exponential(input: [u8], at: i64, end: i64): Result[(i64, u32), pcm.AudioError] {
  let (next, prefix) = unary(input, at, end, 32)?;
  if (prefix < 2u32) {
    return Ok((next, prefix));
  }
  let (stop, value) = read(input, next, end, prefix as i32 - 1)?;
  return Ok((stop, value | 1u32 << prefix - 1u32));
}

pub function code(input: [u8], at: i64, end: i64, maximum: u32): Result[(i64, u32), pcm.AudioError] {
  if (maximum < 2u32) {
    return read(input, at, end, maximum as i32);
  }
  let width = 32 - maximum.leading_zeros();
  let extras = ((1u64 << width as u64) - maximum as u64 - 1u64) as u32;
  let (next, short) = read(input, at, end, width - 1)?;
  if (short < extras) {
    return Ok((next, short));
  }
  let (stop, bit) = read(input, next, end, 1)?;
  return Ok((stop, (short << 1u32) - extras + bit));
}
```
