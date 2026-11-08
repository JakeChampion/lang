# Bounded CAF chunks

This module reads Core Audio Format version-one chunks from a complete immutable byte view.
Chunk sizes use signed 64-bit fields. Only a final data chunk can use an unknown size.
Known singleton chunks must occur at most once. The description must be the first chunk.
Unknown chunks retain their validated extents and need no payload allocation.

```fern
<<*>>=
// Read checked CAF chunk extents without payload allocation.

import "std/audio/pcm";
import "std/audio/wire";

pub struct Chunk { start: i32, end: i32 }

pub struct File { description: Chunk, cookie: Option[Chunk], packets: Option[Chunk], channels: Option[Chunk], data: Chunk }

function one(previous: Option[Chunk], current: Chunk): Result[Option[Chunk], pcm.AudioError] {
  if let Some(_) = previous {
    return Err(pcm.Header);
  }
  return Ok(Some(current));
}

// Check signed sizes, singleton chunks, and the required first description together.
// fern-lint: allow cyclomatic-complexity
pub function inspect(input: [u8], maximum: i32): Result[File, pcm.AudioError] {
  if (maximum < 1) {
    return Err(pcm.Limit);
  }
  if (input.len() < 8) {
    return Err(pcm.Truncated);
  }
  if (!wire.tag(input, 0, "caff")) {
    return Err(pcm.Header);
  }
  if (wire.be(input, 4, 2) != 1u64 || wire.be(input, 6, 2) != 0u64) {
    return Err(pcm.Unsupported);
  }
  let description: Option[Chunk] = None;
  let cookie: Option[Chunk] = None;
  let packets: Option[Chunk] = None;
  let channels: Option[Chunk] = None;
  let data: Option[Chunk] = None;
  let at = 8;
  let count = 0;
  while (at < input.len()) {
    if (count >= maximum) {
      return Err(pcm.Limit);
    }
    if (input.len() - at < 12) {
      return Err(pcm.Truncated);
    }
    let size = wire.be(input, at + 4, 8) as i64;
    let start = at + 12;
    let end = input.len();
    if (size < 0i64) {
      if (size != -1i64 || !wire.tag(input, at, "data")) {
        return Err(pcm.Header);
      }
    } else {
      if (size > (input.len() - start) as i64) {
        return Err(pcm.Truncated);
      }
      end = start + size as i32;
    }
    let chunk = Chunk { start: start, end: end };
    if (count == 0 && !wire.tag(input, at, "desc")) {
      return Err(pcm.Header);
    }
    if (wire.tag(input, at, "desc")) {
      description = one(description, chunk)?;
    } else if (wire.tag(input, at, "kuki")) {
      cookie = one(cookie, chunk)?;
    } else if (wire.tag(input, at, "pakt")) {
      packets = one(packets, chunk)?;
    } else if (wire.tag(input, at, "chan")) {
      channels = one(channels, chunk)?;
    } else if (wire.tag(input, at, "data")) {
      data = one(data, chunk)?;
    }
    at = end;
    count += 1;
  }
  let Some(desc) = description else {
    return Err(pcm.Header);
  };
  let Some(audio) = data else {
    return Err(pcm.Header);
  };
  if (audio.end - audio.start < 4) {
    return Err(pcm.Truncated);
  }
  if (wire.be(input, audio.start, 4) != 0u64) {
    return Err(pcm.Unsupported);
  }
  return Ok(File { description: desc, cookie: cookie, packets: packets, channels: channels, data: Chunk { start: audio.start + 4, end: audio.end } });
}
```
