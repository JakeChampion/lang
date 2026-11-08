# Vorbis least-significant-bit input

This module reads packet bits in Vorbis order.
Reads accept zero through 32 bits and validate the cursor before access.
An incomplete read returns `Truncated` without exposing a partial value.

Reader values retain no packet storage. A caller must stop its decode path after an input error.
This module is available under MPL-2.0. See `licenses/vorbis/NOTICE.txt`.

```fern
<<*>>=
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/vorbis/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.
import "std/audio/pcm";

pub struct Reader { at: i32, bit: i32 }

pub function create(): Reader {
  return Reader { at: 0, bit: 0 };
}

pub function left(input: [u8], reader: Reader): i64 {
  return (input.len() as i64 - reader.at as i64) * 8i64 - reader.bit as i64;
}

pub function valid(input: [u8], reader: Reader): boolean {
  return reader.at >= 0 && reader.at <= input.len() && reader.bit >= 0 && reader.bit < 8 && (reader.at < input.len() || reader.bit == 0);
}

pub function skip(input: [u8], reader: Reader, count: i32): Result[Reader, pcm.AudioError] {
  if (!valid(input, reader) || count < 0) {
    return Err(pcm.Header);
  }
  if (left(input, reader) < count as i64) {
    return Err(pcm.Truncated);
  }
  let tail = reader.bit + count % 8;
  return Ok(Reader { at: reader.at + count / 8 + tail / 8, bit: tail % 8 });
}

pub function read(input: [u8], reader: Reader, count: i32): Result[(Reader, u32), pcm.AudioError] {
  if (!valid(input, reader) || count < 0 || count > 32) {
    return Err(pcm.Header);
  }
  if (left(input, reader) < count as i64) {
    return Err(pcm.Truncated);
  }
  let value = 0u32;
  let used = 0;
  while (used < count) {
    let width = if (count - used < 8 - reader.bit) { count - used } else { 8 - reader.bit };
    let chunk = input[reader.at] as u32 >> reader.bit as u32 & (1u32 << width as u32) - 1u32;
    value |= chunk << used as u32;
    reader = Reader { at: reader.at + (reader.bit + width) / 8, bit: (reader.bit + width) % 8 };
    used += width;
  }
  return Ok((reader, value));
}

pub function wide(input: [u8], reader: Reader, count: i32): Result[(Reader, u64), pcm.AudioError] {
  if (count < 0 || count > 64) {
    return Err(pcm.Header);
  }
  let low_bits = if (count < 32) { count } else { 32 };
  let (next, low) = read(input, reader, low_bits)?;
  if (count <= 32) {
    return Ok((next, low as u64));
  }
  let (end, high) = read(input, next, count - 32)?;
  return Ok((end, low as u64 | high as u64 << 32u64));
}
```
