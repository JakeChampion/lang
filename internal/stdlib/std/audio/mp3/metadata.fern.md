# MP3 container metadata

This helper validates ID3 headers and bounded MP3 trailers.
It supports the metadata rules of `std/audio/mp3` for whole input and incremental byte sources.

`inspect_tail` describes trailing tag sizes from the final 160 bytes or the complete file when shorter.
Seekable callers verify the optional APE header at the returned tag boundary.

```fern
<<*>>=
// Validate MP3 metadata without retaining its payload.

import "std/audio/pcm";
import "std/audio/wire";

function id3_footer(input: [u8], at: i32, size: i32): Result[void, pcm.AudioError] {
  let f = at + 10 + size;
  if (!wire.tag(input, f, "3DI")) {
    return Err(pcm.Header);
  }
  for i in 3..10 {
    if (input[at + i] != input[f + i]) {
      return Err(pcm.Header);
    }
  }
  return Ok(());
}

function id3_size(input: [u8], at: i32): Result[(i32, boolean), pcm.AudioError] {
  let version = input[at + 3] as i32;
  let flags = input[at + 5] as i32;
  if (version < 2 || version > 4 || input[at + 4] == 255) {
    return Err(pcm.Unsupported);
  }
  let reserved = if (version == 2) { 63 } else if (version == 3) { 31 } else { 15 };
  if ((flags & reserved) != 0) {
    return Err(pcm.Header);
  }
  let size = 0;
  for i in 6..10 {
    if (input[at + i] > 127) {
      return Err(pcm.Header);
    }
    size = size << 7 | input[at + i] as i32;
  }
  return Ok((size, version == 4 && (flags & 16) != 0));
}

pub function audio_start(input: [u8], limits: pcm.Limits): Result[i32, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  let at = 0;
  while (wire.tag(input, at, "ID3")) {
    if (input.len() - at < 10) {
      return Err(pcm.Truncated);
    }
    let (size, footer) = id3_size(input, at)?;
    let total = 10 + size + (if (footer) { 10 } else { 0 });
    if (total > input.len() - at) {
      return Err(pcm.Truncated);
    }
    if (footer) {
      id3_footer(input, at, size)?;
    }
    at += total;
  }
  return Ok(at);
}

pub function audio_end(input: [u8]): Result[i32, pcm.AudioError] {
  let end = input.len();
  if (end >= 128 && wire.tag(input, end - 128, "TAG")) {
    end -= 128;
  }
  return ape_start(input, end);
}

function ape_start(input: [u8], own end: i32): Result[i32, pcm.AudioError] {
  if (end >= 32 && wire.tag(input, end - 32, "APETAGEX")) {
    let at = end - 32;
    let footer = ape_footer(input[at:end], end as i64)?;
    end -= footer.bytes as i32;
    if (footer.header) {
      if (end < 32 || !wire.tag(input, end - 32, "APETAGEX")) {
        return Err(pcm.Header);
      }
      end -= 32;
    }
  }
  return Ok(end);
}

struct Ape { bytes: i64, header: boolean }

function ape_footer(input: [u8], end: i64): Result[Ape, pcm.AudioError] {
  if (input.len() != 32 || !wire.tag(input, 0, "APETAGEX")) {
    return Err(pcm.Header);
  }
  let version = wire.le(input, 8, 4);
  let bytes = wire.le(input, 12, 4) as i64;
  if (version != 1000u64 && version != 2000u64 || bytes < 32i64 || bytes > end) {
    return Err(pcm.Header);
  }
  return Ok(Ape { bytes: bytes, header: (wire.le(input, 20, 4) & 2147483648u64) != 0u64 });
}

// Describe trailing tags from the final min(160, input_bytes) file bytes.
pub struct Tail { bytes: i64, header: boolean }

function trailing_id3v1(input: [u8]): boolean {
  return input.len() >= 128 && wire.tag(input, input.len() - 128, "TAG");
}

pub function inspect_tail(input: [u8], input_bytes: i64): Result[Tail, pcm.AudioError] {
  if (input_bytes < 0i64) {
    return Err(pcm.Header);
  }
  let needed = if (input_bytes < 160i64) { input_bytes as i32 } else { 160 };
  if (input.len() != needed) {
    return Err(pcm.Truncated);
  }
  let at = input.len();
  let removed = 0i64;
  if (trailing_id3v1(input)) {
    at -= 128;
    removed = 128i64;
  }
  if (at < 32 || !wire.tag(input, at - 32, "APETAGEX")) {
    return Ok(Tail { bytes: removed, header: false });
  }
  let footer = ape_footer(input[at - 32:at], input_bytes - removed)?;
  let bytes = removed + footer.bytes + (if (footer.header) { 32i64 } else { 0i64 });
  if (bytes > input_bytes) {
    return Err(pcm.Header);
  }
  return Ok(Tail { bytes: bytes, header: footer.header });
}

pub struct Id3 { payload_bytes: i32, footer: boolean }

pub function inspect_id3(input: [u8]): Result[Id3, pcm.AudioError] {
  if (input.len() < 10) {
    return Err(pcm.Truncated);
  }
  if (!wire.tag(input, 0, "ID3")) {
    return Err(pcm.Header);
  }
  let (bytes, footer) = id3_size(input, 0)?;
  return Ok(Id3 { payload_bytes: bytes, footer: footer });
}

pub function check_footer(input: [u8], header: [u8]): Result[void, pcm.AudioError] {
  if (input.len() < 10 || header.len() != 10) {
    return Err(pcm.Truncated);
  }
  if (!wire.tag(input, 0, "3DI")) {
    return Err(pcm.Header);
  }
  for i in 3..10 {
    if (input[i] != header[i]) {
      return Err(pcm.Header);
    }
  }
  return Ok(());
}

pub struct Trailer { first: u8[], last: u8[], bytes: i64, limit: i64 }

pub function trailer(limit: i64): Result[Trailer, pcm.AudioError] {
  if (limit < 0i64) {
    return Err(pcm.Limit);
  }
  return Ok(Trailer { first: [], last: [], bytes: 0i64, limit: limit });
}

function suffix(previous: [u8], input: [u8]): u8[] {
  let bytes: u8[] = [];
  if (input.len() < 160) {
    let begin = if (previous.len() + input.len() > 160) { previous.len() + input.len() - 160 } else { 0 };
    for i in begin..previous.len() {
      bytes = bytes.append(previous[i]);
    }
  }
  let begin = if (input.len() > 160) { input.len() - 160 } else { 0 };
  for i in begin..input.len() {
    bytes = bytes.append(input[i]);
  }
  return bytes;
}

pub function (own state: Trailer) feed(input: [u8]): Result[Trailer, pcm.AudioError] {
  if (input.len() as i64 > state.limit - state.bytes) {
    return Err(pcm.Limit);
  }
  let first = state.first;
  let count = if (input.len() < 32 - first.len()) { input.len() } else { 32 - first.len() };
  for i in 0..count {
    first = first.append(input[i]);
  }
  let last = suffix(state.last, input);
  return Ok(Trailer { ...state, first: first, last: last, bytes: state.bytes + input.len() as i64 });
}

function complete_ape(state: Trailer, end: i64, at: i32): Result[void, pcm.AudioError] {
  if (end < 32i64 || at < 32 || !wire.tag(state.last, at - 32, "APETAGEX")) {
    return Err(pcm.Header);
  }
  let footer = ape_footer(state.last[at - 32:at], end)?;
  let prefix = end - footer.bytes;
  if (prefix != (if (footer.header) { 32i64 } else { 0i64 })) {
    return Err(pcm.Header);
  }
  if (footer.header && !wire.tag(state.first, 0, "APETAGEX")) {
    return Err(pcm.Header);
  }
  return Ok(());
}

pub function (state: Trailer) finish(): Result[void, pcm.AudioError] {
  let end = state.bytes;
  let at = state.last.len();
  if (end >= 128i64 && wire.tag(state.last, at - 128, "TAG")) {
    end -= 128i64;
    at -= 128;
  }
  if (end == 0i64) {
    return Ok(());
  }
  return complete_ape(state, end, at);
}
```
