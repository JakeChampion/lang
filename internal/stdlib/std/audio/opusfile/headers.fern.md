# Ogg Opus headers

This module reads Opus identification and comment packets from RFC 7845.
It accepts channel mapping families 0, 1, and 255.
Family 1 uses Vorbis channel order. Family 255 declares no speaker positions.
Unknown mapping families and incompatible header versions return `Unsupported`.

The input sample rate is metadata. Opus playback uses a supported decoder rate.
The pre-skip count uses 48 kHz units. Output gain uses signed Q7.8 decibels.

Comment values remain bytes, including embedded zero bytes and invalid UTF-8.
`values` compares field names without ASCII case sensitivity.
The parser retains binary suffix data when its first bit is one.
It discards ordinary comment padding.

```fern
<<*>>=
// Opus identification and bounded comment headers from RFC 7845.

import "std/audio/pcm";
import "std/audio/wire";

pub struct Header {
  version: i32,
  channels: i32,
  pre_skip: i32,
  input_sample_rate: u32,
  output_gain: i32,
  mapping_family: i32,
  streams: i32,
  coupled: i32,
  mapping: u8[],
}

pub struct TagLimits { max_bytes: i32, max_comments: i32 }

pub struct Tags { vendor: u8[], comments: u8[][], suffix: u8[] }

pub function default_tag_limits(): TagLimits {
  return TagLimits { max_bytes: 1048576, max_comments: 4096 };
}

function copied(input: [u8]): u8[] {
  let out: u8[] = [];
  for byte in input {
    out = out.append(byte);
  }
  return out;
}

function mapped(input: [u8], channels: i32, version: i32): Result[(i32, i32, u8[]), pcm.AudioError] {
  if (input.len() < 21 + channels) {
    return Err(pcm.Truncated);
  }
  if (version <= 1 && input.len() != 21 + channels) {
    return Err(pcm.Header);
  }
  let streams = input[19] as i32;
  let coupled = input[20] as i32;
  if (streams == 0 || coupled > streams || streams + coupled > 255) {
    return Err(pcm.Header);
  }
  return Ok((streams, coupled, indices(input, channels, streams + coupled)?));
}

function indices(input: [u8], channels: i32, decoded: i32): Result[u8[], pcm.AudioError] {
  let map: u8[] = [];
  for i in 0..channels {
    let index = input[21 + i] as i32;
    if (index != 255 && index >= decoded) {
      return Err(pcm.Header);
    }
    map = map.append(index as u8);
  }
  return Ok(map);
}

function mapping(input: [u8], channels: i32, family: i32, version: i32): Result[(i32, i32, u8[]), pcm.AudioError] {
  if (family == 0) {
    if (channels > 2 || version <= 1 && input.len() != 19) {
      return Err(pcm.Header);
    }
    return Ok((1, channels - 1, if (channels == 1) { [0u8] } else { [0u8, 1u8] }));
  }
  if (family != 1 && family != 255) {
    return Err(pcm.Unsupported);
  }
  if (family == 1 && channels > 8) {
    return Err(pcm.Header);
  }
  return mapped(input, channels, version);
}

pub function inspect(input: [u8]): Result[Header, pcm.AudioError] {
  if (input.len() < 19) {
    return Err(pcm.Truncated);
  }
  if (!wire.tag(input, 0, "OpusHead")) {
    return Err(pcm.Header);
  }
  let version = input[8] as i32;
  if (version > 15) {
    return Err(pcm.Unsupported);
  }
  let channels = input[9] as i32;
  if (channels == 0) {
    return Err(pcm.Header);
  }
  let family = input[18] as i32;
  let (streams, coupled, map) = mapping(input, channels, family, version)?;
  return Ok(Header {
    version: version, channels: channels, pre_skip: wire.le(input, 10, 2) as i32,
    input_sample_rate: wire.le(input, 12, 4) as u32,
    output_gain: (wire.le(input, 16, 2) as i32 ^ 32768) - 32768,
    mapping_family: family, streams: streams, coupled: coupled, mapping: map,
  });
}

function field(input: [u8], at: i32): Result[(u8[], i32), pcm.AudioError] {
  if (input.len() - at < 4) {
    return Err(pcm.Truncated);
  }
  let size = wire.le(input, at, 4);
  let start = at + 4;
  if (size > (input.len() - start) as u64) {
    return Err(pcm.Truncated);
  }
  let end = start + size as i32;
  return Ok((copied(input[start:end]), end));
}

function tag_start(input: [u8], limits: TagLimits): Result[(u8[], i32), pcm.AudioError] {
  if (limits.max_bytes < 0 || limits.max_comments < 0 || input.len() > limits.max_bytes) {
    return Err(pcm.Limit);
  }
  if (input.len() < 16) {
    return Err(pcm.Truncated);
  }
  if (!wire.tag(input, 0, "OpusTags")) {
    return Err(pcm.Header);
  }
  return field(input, 8);
}

pub function tags(input: [u8], limits: TagLimits): Result[Tags, pcm.AudioError] {
  let (vendor, at) = tag_start(input, limits)?;
  if (input.len() - at < 4) {
    return Err(pcm.Truncated);
  }
  let count = wire.le(input, at, 4);
  at += 4;
  if (count > ((input.len() - at) / 4) as u64) {
    return Err(pcm.Truncated);
  }
  if (count > limits.max_comments as u64) {
    return Err(pcm.Limit);
  }
  let comments: u8[][] = [];
  for _ in 0..count as i32 {
    let (comment, end) = field(input, at)?;
    comments = comments.append(comment);
    at = end;
  }
  let suffix: u8[] = [];
  if (at < input.len() && (input[at] & 1u8) != 0u8) {
    suffix = copied(input[at:]);
  }
  return Ok(Tags { vendor: vendor, comments: comments, suffix: suffix });
}

function folded(byte: u8): u8 {
  return if (byte >= 65u8 && byte <= 90u8) { byte + 32u8 } else { byte };
}

function named(comment: [u8], key: [u8]): boolean {
  if (comment.len() <= key.len() || comment[key.len()] != 61u8) {
    return false;
  }
  for i in 0..key.len() {
    if (folded(comment[i]) != folded(key[i])) {
      return false;
    }
  }
  return true;
}

pub function values(tags: Tags, key: string): u8[][] {
  let found: u8[][] = [];
  let name = key.as_bytes();
  for comment in tags.comments {
    if (named(comment, name)) {
      found = found.append(copied(comment[name.len() + 1:]));
    }
  }
  return found;
}
```
