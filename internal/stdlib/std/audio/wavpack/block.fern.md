# WavPack blocks

This module validates WavPack version 4 block headers and metadata boundaries.
Metadata entries retain byte offsets into the caller input.
The module does not copy the input or decode samples.
It checks a declared block checksum before it returns metadata.

```fern
<<*>>=
// Validated WavPack block headers and metadata spans.
// The checksum derives from WavPack 5.8.1, open_utils.c.
// Copyright (c) 1998 - 2025 David Bryant. BSD-3-Clause.
// See licenses/wavpack/BSD-3-Clause.txt.
import "std/audio/pcm";
import "std/audio/wire";

pub struct Entry { id: i32, at: i32, size: i32 }

pub struct Block { at: i32, end: i32, version: i32, index: i64, total: i64, frames: i32, flags: u32, crc: u32, metadata: Entry[] }

function checksum(input: [u8], first: i32, stop: i32, entry: Entry): Result[void, pcm.AudioError] {
  if (entry.size != 2 && entry.size != 4 || (stop - first) % 2 != 0) {
    return Err(pcm.Header);
  }
  let sum = 4294967295u32;
  let at = first;
  while (at < stop) {
    sum = sum * 3u32 + wire.le(input, at, 2) as u32;
    at += 2;
  }
  if (entry.size == 2) {
    sum = (sum ^ sum >> 16u32) & 65535u32;
  }
  if (sum != wire.le(input, entry.at, entry.size) as u32) {
    return Err(pcm.Checksum);
  }
  return Ok(());
}

function extent(input: [u8], at: i32): Result[i32, pcm.AudioError] {
  if (at < 0 || at > input.len()) {
    return Err(pcm.Limit);
  }
  if (input.len() - at < 32) {
    return Err(pcm.Truncated);
  }
  if (!wire.tag(input, at, "wvpk")) {
    return Err(pcm.Header);
  }
  let size = wire.le(input, at + 4, 4) + 8u64;
  if (size < 32u64 || size % 2u64 != 0u64) {
    return Err(pcm.Header);
  }
  if (size > (input.len() - at) as u64) {
    return Err(pcm.Truncated);
  }
  return Ok(at + size as i32);
}

function header(input: [u8], at: i32, limits: pcm.Limits): Result[Block, pcm.AudioError] {
  let end = extent(input, at)?;
  let version = wire.le(input, at + 8, 2) as i32;
  if (version < 1026 || version > 1040) {
    return Err(pcm.Unsupported);
  }
  let frames = wire.le(input, at + 20, 4);
  if (frames > limits.max_samples as u64) {
    return Err(pcm.Limit);
  }
  let index = wire.le(input, at + 16, 4) as i64 + (input[at + 10] as i64 << 32i64);
  let total_lo = wire.le(input, at + 12, 4);
  let total = if (total_lo == 4294967295u64) { -1i64 } else { total_lo as i64 + input[at + 11] as i64 * 4294967295i64 };
  if (total >= 0i64 && (index > total || frames as i64 > total - index)) {
    return Err(pcm.Header);
  }
  let flags = wire.le(input, at + 24, 4) as u32;
  return Ok(Block { at: at, end: end, version: version, index: index, total: total, frames: frames as i32, flags: flags, crc: wire.le(input, at + 28, 4) as u32, metadata: [] });
}

function next_entry(input: [u8], cursor: i32, end: i32): Result[(i32, Entry, i32), pcm.AudioError] {
  if (end - cursor < 2) {
    return Err(pcm.Truncated);
  }
  let tag = input[cursor] as i32;
  let words = input[cursor + 1] as i32;
  cursor += 2;
  if ((tag & 128) != 0) {
    if (end - cursor < 2) {
      return Err(pcm.Truncated);
    }
    words += (input[cursor] as i32 << 8) + (input[cursor + 1] as i32 << 16);
    cursor += 2;
  }
  let bytes = words * 2;
  let odd = tag >> 6 & 1;
  if (bytes < odd) {
    return Err(pcm.Header);
  }
  if (bytes > end - cursor) {
    return Err(pcm.Truncated);
  }
  return Ok((cursor + bytes, Entry { id: tag & 63, at: cursor, size: bytes - odd }, tag));
}

function check_entry(input: [u8], value: Block, entry: Entry, start: i32, stop: i32, tag: i32, checked: boolean): Result[boolean, pcm.AudioError] {
  if (entry.id != 47) {
    return Ok(checked);
  }
  if (checked || (tag & 192) != 0 || stop != value.end) {
    return Err(pcm.Header);
  }
  checksum(input, value.at, start, entry)?;
  return Ok(true);
}

pub function read(input: [u8], at: i32, max_metadata: i32, limits: pcm.Limits): Result[Block, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (max_metadata < 0) {
    return Err(pcm.Limit);
  }
  let value = header(input, at, limits)?;
  let metadata: Entry[] = [];
  let cursor = at + 32;
  let checked = false;
  while (cursor < value.end) {
    if (metadata.len() >= max_metadata) {
      return Err(pcm.Limit);
    }
    let start = cursor;
    let (stop, entry, tag) = next_entry(input, cursor, value.end)?;
    checked = check_entry(input, value, entry, start, stop, tag, checked)?;
    metadata = metadata.append(entry);
    cursor = stop;
  }
  if ((value.flags & 268435456u32) != 0u32 && !checked) {
    return Err(pcm.Checksum);
  }
  return Ok(Block { ...value, metadata: metadata });
}
```
