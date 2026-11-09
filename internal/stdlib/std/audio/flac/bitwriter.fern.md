# Bounded FLAC bit output

This module writes most-significant-bit-first fields into a preallocated frame buffer.
The frame plan supplies the exact byte extent before allocation.
The header occupies the first complete bytes. Two bytes remain for the final CRC-16.
Invalid fields or extents mark the writer as failed and never write outside its buffer.
The frame buffer cannot exceed four MiB.

```fern
<<*>>=
// Write bounded FLAC fields and append the standard frame checksum.

import "std/audio/flac/checksum";
import "std/audio/pcm";

pub opaque struct BitWriter { data: u8[], at: i32, bit: i32, bad: boolean }

pub function create(bytes: i32, header: [u8]): Result[BitWriter, pcm.AudioError] {
  if (bytes > 4194304) {
    return Err(pcm.Limit);
  }
  if (bytes < 2 || header.len() > bytes - 2) {
    return Err(pcm.Header);
  }
  let data = __alloc_u8(bytes);
  for i in 0..header.len() {
    data = data.with(i, header[i]);
  }
  return Ok(BitWriter { data: data, at: header.len(), bit: 0, bad: false });
}

pub function (own writer: BitWriter) put(value: u64, count: i32): BitWriter {
  let BitWriter { data, at, bit, bad } = writer;
  if (bad || count < 0 || count > 33 || count as i64 > (data.len() - 2 - at) as i64 * 8i64 - bit as i64) {
    return BitWriter { data: data, at: at, bit: bit, bad: true };
  }
  let left = count;
  while (left > 0) {
    let take = if (left < 8 - bit) { left } else { 8 - bit };
    let part = (value >> (left - take) as u64 & (1u64 << take as u64) - 1u64) as u8;
    data = data.with(at, data[at] | part << (8 - bit - take) as u8);
    left -= take;
    let next = bit + take;
    at += next / 8;
    bit = next % 8;
  }
  return BitWriter { data: data, at: at, bit: bit, bad: false };
}

pub function (own writer: BitWriter) zeros(count: u64): BitWriter {
  let available = (writer.data.len() - 2 - writer.at) as i64 * 8i64 - writer.bit as i64;
  if (writer.bad || available < 0i64 || count > available as u64) {
    return BitWriter { ...writer, bad: true };
  }
  let position = writer.at as i64 * 8i64 + writer.bit as i64 + count as i64;
  return BitWriter { ...writer, at: (position / 8i64) as i32, bit: (position % 8i64) as i32 };
}

pub function (own writer: BitWriter) finish(): Result[u8[], pcm.AudioError] {
  let BitWriter { data, at, bit, bad } = writer;
  at += if (bit == 0) { 0 } else { 1 };
  if (bad || at != data.len() - 2) {
    return Err(pcm.Header);
  }
  let crc = checksum.crc(data[:at], true);
  data = data.with(at, (crc >> 8) as u8);
  data = data.with(at + 1, crc as u8);
  return Ok(data);
}
```
