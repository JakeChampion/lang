# WavPack trailing tags

This module locates audio before bounded APEv1, APEv2, and ID3v1 tags.
It validates item lengths and optional APEv2 headers.
It does not copy or interpret tag values.

```fern
<<*>>=
// Validated trailing tag boundaries for WavPack files.
import "std/audio/pcm";
import "std/audio/wire";

function key_end(input: [u8], key: i32, end: i32): Result[i32, pcm.AudioError] {
  let stop = key;
  while (stop < end && input[stop] != 0u8) {
    if (stop - key >= 255) {
      return Err(pcm.Header);
    }
    if (input[stop] < 32u8 || input[stop] > 126u8) {
      return Err(pcm.Header);
    }
    stop += 1;
  }
  if (stop == end || stop == key) {
    return Err(pcm.Header);
  }
  return Ok(stop);
}

function item(input: [u8], at: i32, end: i32): Result[i32, pcm.AudioError] {
  if (end - at < 9) {
    return Err(pcm.Header);
  }
  let size = wire.le(input, at, 4);
  let flags = wire.le(input, at + 4, 4);
  if (flags > 7u64) {
    return Err(pcm.Header);
  }
  let stop = key_end(input, at + 8, end)?;
  if (size > (end - stop - 1) as u64) {
    return Err(pcm.Header);
  }
  return Ok(stop + 1 + size as i32);
}

function items(input: [u8], at: i32, end: i32, count: i32): Result[void, pcm.AudioError] {
  for _ in 0..count {
    at = item(input, at, end)?;
  }
  if (at != end) {
    return Err(pcm.Header);
  }
  return Ok(());
}

function footer(input: [u8], at: i32, max_items: i32): Result[(i32, i32, boolean), pcm.AudioError] {
  let version = wire.le(input, at + 8, 4);
  let size = wire.le(input, at + 12, 4);
  let count = wire.le(input, at + 16, 4);
  let flags = wire.le(input, at + 20, 4);
  if (version != 1000u64 && version != 2000u64) {
    return Err(pcm.Unsupported);
  }
  if (size < 32u64 || size > (at + 32) as u64) {
    return Err(pcm.Header);
  }
  if (count > max_items as u64) {
    return Err(pcm.Limit);
  }
  if ((flags & 2147483647u64) != 0u64 || wire.le(input, at + 24, 8) != 0u64) {
    return Err(pcm.Header);
  }
  return Ok((at + 32 - size as i32, count as i32, (flags & 2147483648u64) != 0u64));
}

function header(input: [u8], begin: i32, end: i32): Result[i32, pcm.AudioError] {
  if (begin < 32 || !wire.tag(input, begin - 32, "APETAGEX")) {
    return Err(pcm.Header);
  }
  for i in 8..32 {
    let expected = input[end + i] | (if (i == 23) { 32u8 } else { 0u8 });
    if (input[begin - 32 + i] != expected) {
      return Err(pcm.Header);
    }
  }
  return Ok(begin - 32);
}

pub function audio_end(input: [u8], max_items: i32): Result[i32, pcm.AudioError] {
  if (max_items < 0) {
    return Err(pcm.Limit);
  }
  let end = input.len();
  if (end >= 128 && wire.tag(input, end - 128, "TAG")) {
    end -= 128;
  }
  if (end < 32) {
    return Ok(end);
  }
  let stop = end - 32;
  if (!wire.tag(input, stop, "APETAGEX")) {
    return Ok(end);
  }
  let (begin, count, has_header) = footer(input, stop, max_items)?;
  items(input, begin, stop, count)?;
  if (has_header) {
    return header(input, begin, stop);
  }
  return Ok(begin);
}
```
