# Vorbis identification and comments

This module reads the Vorbis identification and comment packets.
Identification validates channel count, sample rate, block sizes, and framing.
Comment metadata retains its exact bytes, including non-UTF-8 text.

The comment packet permits padding after its framing bit.
Input and metadata limits apply before the module copies fields.
This module is available under MPL-2.0. See `licenses/vorbis/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's Vorbis identification header reader.
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/vorbis/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.
import "std/audio/vorbis/types";
import "std/audio/pcm";
import "std/audio/wire";

pub struct Tags { vendor: u8[], comments: u8[][] }

pub function signature(input: [u8], kind: u8): Result[void, pcm.AudioError] {
  if (input.len() < 7) {
    return Err(pcm.Truncated);
  }
  if (input[0] != kind || !wire.tag(input, 1, "vorbis")) {
    return Err(pcm.Header);
  }
  return Ok(());
}

// Validate fixed identification fields before constructing stream state.
// fern-lint: allow cyclomatic-complexity
pub function identification(input: [u8], limits: types.Limits): Result[types.Info, pcm.AudioError] {
  if (!types.valid(limits)) {
    return Err(pcm.Limit);
  }
  pcm.check_input(input, limits.audio)?;
  signature(input, 1u8)?;
  if (input.len() < 30) {
    return Err(pcm.Truncated);
  }
  if (input.len() != 30) {
    return Err(pcm.Header);
  }
  if (wire.le(input, 7, 4) != 0u64) {
    return Err(pcm.Unsupported);
  }
  let channels = input[11] as i32;
  let rate = wire.le(input, 12, 4);
  if (channels == 0 || rate == 0u64) {
    return Err(pcm.Header);
  }
  if (channels > 8 || rate > 16777215u64) {
    return Err(pcm.Unsupported);
  }
  let small = input[28] as i32 & 15;
  let large = input[28] as i32 >> 4;
  if (small < 6 || small > 13 || large < small || large > 13 || input[29] != 1u8) {
    return Err(pcm.Header);
  }
  return Ok(types.Info { sample_rate: rate as i32, channels: channels, small_exponent: small, large_exponent: large, bitrate_upper: wire.le(input, 16, 4) as i32, bitrate_nominal: wire.le(input, 20, 4) as i32, bitrate_lower: wire.le(input, 24, 4) as i32 });
}

function copied(input: [u8]): u8[] {
  let value: u8[] = [];
  for byte in input {
    value = value.append(byte);
  }
  return value;
}

function field(input: [u8], at: i32): Result[(u8[], i32), pcm.AudioError] {
  if (at < 0 || at > input.len()) {
    return Err(pcm.Header);
  }
  if (input.len() - at < 4) {
    return Err(pcm.Truncated);
  }
  let count = wire.le(input, at, 4);
  let start = at + 4;
  if (count > (input.len() - start) as u64) {
    return Err(pcm.Truncated);
  }
  let end = start + count as i32;
  return Ok((copied(input[start:end]), end));
}

// Keep exact-byte metadata parsing beside count and extent bounds.
// fern-lint: allow cyclomatic-complexity
pub function comments(input: [u8], limits: types.Limits): Result[Tags, pcm.AudioError] {
  if (!types.valid(limits) || input.len() > limits.max_comment_bytes) {
    return Err(pcm.Limit);
  }
  pcm.check_input(input, limits.audio)?;
  signature(input, 3u8)?;
  let (vendor, at) = field(input, 7)?;
  if (input.len() - at < 4) {
    return Err(pcm.Truncated);
  }
  let count = wire.le(input, at, 4);
  at += 4;
  if (count > limits.max_comments as u64) {
    return Err(pcm.Limit);
  }
  if (count > ((input.len() - at) / 4) as u64) {
    return Err(pcm.Truncated);
  }
  let comments: u8[][] = [];
  for _ in 0..count as i32 {
    let (comment, next) = field(input, at)?;
    comments = comments.append(comment);
    at = next;
  }
  if (at == input.len()) {
    return Err(pcm.Truncated);
  }
  if ((input[at] & 1u8) == 0u8) {
    return Err(pcm.Header);
  }
  return Ok(Tags { vendor: vendor, comments: comments });
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
  let out: u8[][] = [];
  let name = key.as_bytes();
  for comment in tags.comments {
    if (named(comment, name)) {
      out = out.append(copied(comment[name.len() + 1:]));
    }
  }
  return out;
}
```
