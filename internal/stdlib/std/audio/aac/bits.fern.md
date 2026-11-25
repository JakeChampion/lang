# Bounded AAC bit input

This module reads most-significant-bit-first AAC syntax and fixed Huffman codebooks.
Every operation checks its input range. Incomplete codewords return `Truncated`.
Reader positions contain integers and retain no encoded input.

The generated prefix tables use nine bits. Longer codewords use a bounded tree walk.
The ADTS CRC is the CRC-16 of ISO/IEC 13818-7: polynomial 0x8005, register preset to ones, no final inversion.
This module is available under MPL-2.0. See `licenses/aac/NOTICE.txt`.

```fern
<<*>>=
// Copyright (c) 2019-2023 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/aac/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.

import "std/audio/pcm";
import "std/audio/aac/tables";

pub struct Reader { at: i32, bit: i32 }

pub function create(): Reader {
  return Reader { at: 0, bit: 0 };
}

pub function left(input: [u8], reader: Reader): i64 {
  return (input.len() as i64 - reader.at as i64) * 8i64 - reader.bit as i64;
}

function valid(input: [u8], reader: Reader): boolean {
  return reader.at >= 0 && reader.at <= input.len() && reader.bit >= 0 && reader.bit < 8 && (reader.at < input.len() || reader.bit == 0);
}

function advanced(reader: Reader, count: i32): Reader {
  let tail = reader.bit + count % 8;
  return Reader { at: reader.at + count / 8 + tail / 8, bit: tail % 8 };
}

pub function read(input: [u8], reader: Reader, count: i32): Result[(Reader, u32), pcm.AudioError] {
  if (!valid(input, reader) || count < 0 || count > 32) {
    return Err(pcm.Header);
  }
  if (left(input, reader) < count as i64) {
    return Err(pcm.Truncated);
  }
  let value = 0u32;
  let remaining = count;
  while (remaining > 0) {
    let take = if (8 - reader.bit < remaining) { 8 - reader.bit } else { remaining };
    let mask = (1u32 << take as u32) - 1u32;
    value = value << take as u32 | input[reader.at] as u32 >> (8 - reader.bit - take) as u32 & mask;
    reader = advanced(reader, take);
    remaining -= take;
  }
  return Ok((reader, value));
}

pub function skip(input: [u8], reader: Reader, count: i32): Result[Reader, pcm.AudioError] {
  if (!valid(input, reader) || count < 0) {
    return Err(pcm.Header);
  }
  if (left(input, reader) < count as i64) {
    return Err(pcm.Truncated);
  }
  return Ok(Reader { at: reader.at + count / 8 + (reader.bit + count % 8) / 8, bit: (reader.bit + count % 8) % 8 });
}

pub function align(input: [u8], reader: Reader): Result[Reader, pcm.AudioError] {
  return skip(input, reader, (8 - reader.bit) % 8);
}

function crc_bit(value: u32, bit: u32): u32 {
  let feedback = value >> 15u32 & 1u32 ^ bit;
  let shifted = value << 1u32 & 65535u32;
  return if (feedback != 0u32) { shifted ^ 32773u32 } else { shifted };
}

// Continue the ADTS CRC-16 over the bits from `from` to `to`.
// A nonzero `max` takes at most `max` bits and pads a shorter span with zero bits.
pub function crc(input: [u8], from: Reader, to: Reader, max: i32, value: u32): Result[u32, pcm.AudioError] {
  let length = (to.at - from.at) * 8 + to.bit - from.bit;
  if (!valid(input, from) || !valid(input, to) || length < 0 || max < 0) {
    return Err(pcm.Header);
  }
  let count = if (max > 0 && length > max) { max } else { length };
  let reader = from;
  for _ in 0..count {
    value = crc_bit(value, input[reader.at] as u32 >> (7 - reader.bit) as u32 & 1u32);
    reader = advanced(reader, 1);
  }
  for _ in count..max {
    value = crc_bit(value, 0u32);
  }
  return Ok(value);
}

function tree(input: [u8], reader: Reader, node: i32): Result[(Reader, i32), pcm.AudioError] {
  for _ in 9..19 {
    let (next, bit) = read(input, reader, 1)?;
    reader = next;
    let child = tables.HUFF_CHILDREN[2 * node + bit as i32];
    if (child == 2147483647) {
      return Err(pcm.Header);
    }
    if (child < 0) {
      return Ok((reader, -child - 1));
    }
    node = child;
  }
  return Err(pcm.Header);
}

// Keep fast-prefix and fallback-tree checks in one bounded decode path.
// fern-lint: allow cyclomatic-complexity
pub function huffman(input: [u8], reader: Reader, book: i32): Result[(Reader, i32), pcm.AudioError] {
  if (book < 0 || book > 11 || !valid(input, reader)) {
    return Err(pcm.Header);
  }
  let available = left(input, reader);
  let count = if (available < 9i64) { available as i32 } else { 9 };
  let (_, prefix) = read(input, reader, count)?;
  let entry = tables.HUFF_FAST[book * 512 + (prefix << (9 - count) as u32) as i32];
  if (entry == 2147483647) {
    return Err(if (count < 9) { pcm.Truncated } else { pcm.Header });
  }
  if (entry >= 0) {
    let length = entry >> 16;
    if (length > count) {
      return Err(pcm.Truncated);
    }
    return Ok((advanced(reader, length), entry & 65535));
  }
  if (count < 9) {
    return Err(pcm.Truncated);
  }
  return tree(input, advanced(reader, 9), -entry - 1);
}
```
