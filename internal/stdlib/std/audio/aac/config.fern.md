# AAC configuration and ADTS headers

This module reads MPEG-4 AudioSpecificConfig and ADTS frame boundaries.
It identifies AAC-LC with 1024 samples per raw data block and declared channel layouts.
Unsupported profiles and frame lengths return an explicit error.

ADTS headers expose protection and raw-data-block counts.
Header inspection does not verify a transport CRC or decode spectral data.
This module is available under MPL-2.0. See `licenses/aac/NOTICE.txt`.

```fern
<<*>>=
// Copyright (c) 2019-2023 The Project Symphonia Developers.
// This Source Code Form is subject to the Mozilla Public License, v. 2.0.
// See licenses/aac/MPL-2.0.txt or https://mozilla.org/MPL/2.0/.

import "std/audio/aac/bits";
import "std/audio/pcm";
import "core/cmp";

@derive(cmp.Eq)
pub struct Config { object_type: i32, sample_rate: i32, rate_index: i32, channel_config: i32, channels: i32, frame_size: i32 }

pub struct Adts { config: Config, bytes: i32, header_bytes: i32, blocks: i32, protected: boolean, mpeg2: boolean }

pub const SAMPLE_RATES: i32[] = [96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350];

const CHANNELS: i32[] = [0, 1, 2, 3, 4, 5, 6, 8];

const THRESHOLDS: i32[] = [92017, 75132, 55426, 46009, 37566, 27713, 23004, 18783, 13856, 11502, 9391, 7513, 0];

pub function rate_index(rate: i32): i32 {
  for i in 0..THRESHOLDS.len() {
    if (rate >= THRESHOLDS[i]) {
      return i;
    }
  }
  return 12;
}

pub function create(object_type: i32, rate: i32, channel_config: i32): Result[Config, pcm.AudioError] {
  if (object_type != 2 || rate < 7350 || rate > 96000) {
    return Err(pcm.Unsupported);
  }
  if (channel_config < 0 || channel_config >= CHANNELS.len()) {
    return Err(pcm.Header);
  }
  if (channel_config == 0) {
    return Err(pcm.Unsupported);
  }
  return Ok(Config { object_type: object_type, sample_rate: rate, rate_index: rate_index(rate), channel_config: channel_config, channels: CHANNELS[channel_config], frame_size: 1024 });
}

function frequency(input: [u8], reader: bits.Reader): Result[(bits.Reader, i32), pcm.AudioError] {
  let (next, index) = bits.read(input, reader, 4)?;
  if (index == 15u32) {
    let (end, rate) = bits.read(input, next, 24)?;
    return Ok((end, rate as i32));
  }
  if (index >= SAMPLE_RATES.len() as u32) {
    return Err(pcm.Header);
  }
  return Ok((next, SAMPLE_RATES[index as i32]));
}

function object_type(input: [u8], reader: bits.Reader): Result[(bits.Reader, i32), pcm.AudioError] {
  let (next, value) = bits.read(input, reader, 5)?;
  if (value != 31u32) {
    return Ok((next, value as i32));
  }
  let (end, extension) = bits.read(input, next, 6)?;
  return Ok((end, 32 + extension as i32));
}

function padding(input: [u8], reader: bits.Reader, config: Config): Result[Config, pcm.AudioError] {
  while (bits.left(input, reader) > 0i64) {
    let left = bits.left(input, reader);
    let (next, value) = bits.read(input, reader, if (left > 32i64) { 32 } else { left as i32 })?;
    reader = next;
    if (value != 0u32) {
      return Err(pcm.Unsupported);
    }
  }
  return Ok(config);
}

// Check the ordered SBR and PS extension fields before accepting AAC-LC.
// fern-lint: allow cyclomatic-complexity
function sync_extension(input: [u8], reader: bits.Reader, config: Config): Result[Config, pcm.AudioError] {
  if (bits.left(input, reader) < 11i64) {
    return padding(input, reader, config);
  }
  let (b1, sync) = bits.read(input, reader, 11)?;
  if (sync == 0u32) {
    return padding(input, b1, config);
  }
  if (sync != 695u32) {
    return Err(pcm.Unsupported);
  }
  let (b2, profile) = object_type(input, b1)?;
  if (profile != 5) {
    return Err(pcm.Unsupported);
  }
  let (b3, sbr) = bits.read(input, b2, 1)?;
  if (sbr != 0u32) {
    return Err(pcm.Unsupported);
  }
  if (bits.left(input, b3) < 12i64) {
    return padding(input, b3, config);
  }
  let (b4, ps_sync) = bits.read(input, b3, 11)?;
  if (ps_sync == 0u32) {
    return padding(input, b4, config);
  }
  if (ps_sync != 1352u32) {
    return Err(pcm.Unsupported);
  }
  let (b5, ps) = bits.read(input, b4, 1)?;
  if (ps != 0u32) {
    return Err(pcm.Unsupported);
  }
  return padding(input, b5, config);
}

// Validate the complete AudioSpecificConfig before exposing its configuration.
// fern-lint: allow cyclomatic-complexity
pub function inspect(input: [u8], limits: pcm.Limits): Result[Config, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (input.len() > 64) {
    return Err(pcm.Limit);
  }
  let (b1, profile) = object_type(input, bits.create())?;
  let (b2, rate) = frequency(input, b1)?;
  let (b3, channels) = bits.read(input, b2, 4)?;
  let config = create(profile, rate, channels as i32)?;
  let (b4, short) = bits.read(input, b3, 1)?;
  let (b5, core) = bits.read(input, b4, 1)?;
  let (b6, extension) = bits.read(input, b5, 1)?;
  if (short != 0u32 || core != 0u32 || extension != 0u32) {
    return Err(pcm.Unsupported);
  }
  return sync_extension(input, b6, config);
}

// Validate transport lengths and channel fields before returning the frame boundary.
// fern-lint: allow cyclomatic-complexity
pub function adts(input: [u8], at: i32, limits: pcm.Limits): Result[Adts, pcm.AudioError] {
  pcm.check_input(input, limits)?;
  if (at < 0 || at > input.len()) {
    return Err(pcm.Header);
  }
  if (input.len() - at < 7) {
    return Err(pcm.Truncated);
  }
  if (input[at] != 255u8 || (input[at + 1] & 246u8) != 240u8) {
    return Err(pcm.Header);
  }
  let protected = (input[at + 1] & 1u8) == 0u8;
  let rate = input[at + 2] as i32 >> 2 & 15;
  if (rate >= SAMPLE_RATES.len()) {
    return Err(pcm.Header);
  }
  let channels = (input[at + 2] as i32 & 1) << 2 | input[at + 3] as i32 >> 6;
  let config = create((input[at + 2] as i32 >> 6) + 1, SAMPLE_RATES[rate], channels)?;
  let blocks = (input[at + 6] as i32 & 3) + 1;
  let header_bytes = 7 + (if (protected) { 2 * blocks } else { 0 });
  let bytes = (input[at + 3] as i32 & 3) << 11 | input[at + 4] as i32 << 3 | input[at + 5] as i32 >> 5;
  if (bytes < header_bytes) {
    return Err(pcm.Header);
  }
  return Ok(Adts { config: config, bytes: bytes, header_bytes: header_bytes, blocks: blocks, protected: protected, mpeg2: (input[at + 1] & 8u8) != 0u8 });
}
```
