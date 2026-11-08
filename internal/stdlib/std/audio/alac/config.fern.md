# ALAC configuration and channel layouts

This module reads the 24-byte ALAC configuration and the optional 24-byte channel-layout extension.
The version-zero decoder supports standard layouts with one through eight channels.
PCM uses signed, right-aligned integers in WAVE speaker order.
The channel mask distinguishes layouts that have the same channel count.

Configuration limits apply before decoder scratch allocation.
This module is available under MPL-2.0. See `licenses/alac/NOTICE.txt`.

```fern
<<*>>=
// Derived from Project Symphonia's ALAC decoder.
// Copyright (c) 2019-2022 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/alac/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.

import "std/audio/pcm";
import "std/audio/wire";

pub struct Info { format: pcm.Format, frame_length: i32, pb: i32, mb: i32, kb: i32, max_run: i32, max_frame_bytes: i64, channel_mask: u32 }

const ORDER: i32[] = [0, 0, 1, 2, 0, 1, 2, 0, 1, 3, 2, 0, 1, 3, 4, 2, 0, 1, 4, 5, 3, 2, 0, 1, 5, 6, 4, 3, 2, 4, 5, 0, 1, 6, 7, 3];

pub function mask(channels: i32): u32 {
  return match (channels) { 1 => 4u32, 2 => 3u32, 3 => 7u32, 4 => 263u32, 5 => 1543u32, 6 => 1551u32, 7 => 1807u32, 8 => 1743u32, _ => 0u32 };
}

pub function channel(channels: i32, index: i32): Result[i32, pcm.AudioError] {
  if (index < 0 || index >= channels || channels < 1 || channels > 8) {
    return Err(pcm.Header);
  }
  return Ok(ORDER[channels * (channels - 1) / 2 + index]);
}

// Validate the complete optional layout record and its declared channel count together.
// fern-lint: allow cyclomatic-complexity
function layout(input: [u8], channels: i32): Result[void, pcm.AudioError] {
  if (input.len() == 24) {
    return Ok(());
  }
  if (wire.be(input, 24, 4) != 24u64 || !wire.tag(input, 28, "chan") || wire.be(input, 32, 4) != 0u64 || wire.be(input, 40, 4) != 0u64 || wire.be(input, 44, 4) != 0u64) {
    return Err(pcm.Header);
  }
  let tag = wire.be(input, 36, 4);
  let expected = match (channels) { 1 => 100, 2 => 101, 3 => 113, 4 => 116, 5 => 120, 6 => 124, 7 => 142, _ => 127 };
  if (tag != (expected as u64 << 16u64 | channels as u64)) {
    return Err(pcm.Unsupported);
  }
  return Ok(());
}

// Check configuration fields and allocation limits before the decoder creates scratch storage.
// fern-lint: allow cyclomatic-complexity
pub function read(input: [u8], limits: pcm.Limits): Result[Info, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (input.len() != 24 && input.len() != 48) {
    return Err(if (input.len() < 24) { pcm.Truncated } else { pcm.Header });
  }
  if (input[4] != 0u8) {
    return Err(pcm.Unsupported);
  }
  let depth = input[5] as i32;
  if (depth != 16 && depth != 20 && depth != 24 && depth != 32) {
    return Err(pcm.Unsupported);
  }
  let channels = input[9] as i32;
  let rate = wire.be(input, 20, 4);
  let frames = wire.be(input, 0, 4);
  if (rate < 1u64 || rate > 16777215u64 || frames < 1u64 || frames > 2147483647u64 || input[8] > 31u8) {
    return Err(pcm.Header);
  }
  let _ = pcm.sample_count(frames as i32, channels, limits)?;
  layout(input, channels)?;
  return Ok(Info { format: pcm.Format { sample_rate: rate as i32, channels: channels, bits_per_sample: depth }, frame_length: frames as i32, pb: input[6] as i32, mb: input[7] as i32, kb: input[8] as i32, max_run: wire.be(input, 10, 2) as i32, max_frame_bytes: wire.be(input, 12, 4) as i64, channel_mask: mask(channels) });
}
```
