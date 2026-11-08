# Native FLAC integer encoding

This module encodes bounded integer PCM with constant, verbatim, and fixed-predictor subframes.
It selects exact Rice/Rice2 partition costs and compares all four stereo assignments.
Common trailing zero bits can reduce the subframe width without changing any sample.
Residual overflow falls back to verbatim coding.

`encode_frame` emits one independent frame with a frame number or sample position.
`stream_header` supplies STREAMINFO for a caller that writes frames incrementally.
`encode` writes a complete fixed-block stream with the exact total count and PCM MD5.
Each encoded frame includes its header CRC-8 and frame CRC-16.

The encoder uses no float conversion or quantization.
This first encoder does not perform LPC analysis or claim a compression or performance lead.
The encoded byte budget uses `pcm.Limits.max_input_bytes`.

```fern
<<*>>=
// Encode standard FLAC frames and complete streams from validated integer PCM.

import "std/audio/pcm";
import "std/audio/wire";
import "std/audio/flac/checksum";
import "std/audio/flac/bitwriter";
import "std/audio/flac/plan";
import "std/crypto";

pub struct Options { block_frames: i32, max_fixed_order: i32, max_partition_order: i32, stereo: boolean }

pub enum Position { FrameNumber(u32), SampleNumber(u64) }

pub struct StreamInfo { format: pcm.Format, frames: u64, min_block: i32, max_block: i32, md5: u8[] }

pub struct EncodedFrame { frames: i32, number: u64, variable: boolean, assignment: i32, bytes: u8[] }

struct Selection { assignment: i32, channels: plan.Channel[], bits: i64 }

pub function default_options(): Options {
  return Options { block_frames: 4096, max_fixed_order: 4, max_partition_order: 6, stereo: true };
}

function valid_format(format: pcm.Format): boolean {
  return pcm.valid(format) && format.bits_per_sample >= 4 && format.sample_rate <= 1048575;
}

function valid_options(options: Options): boolean {
  return options.block_frames >= 16 && options.block_frames <= 65535
    && options.max_fixed_order >= 0 && options.max_fixed_order <= 4
    && options.max_partition_order >= 0 && options.max_partition_order <= 15;
}

pub function stream_header(info: StreamInfo, limits: pcm.Limits): Result[u8[], pcm.AudioError] {
  pcm.check_input([], limits)?;
  if (!valid_format(info.format) || info.frames > 68719476735u64 || info.min_block < 16
    || info.max_block < info.min_block || info.max_block > 65535 || info.md5.len() != 16) {
    return Err(pcm.Header);
  }
  if (limits.max_input_bytes < 42) {
    return Err(pcm.Limit);
  }
  let output: u8[] = [102, 76, 97, 67, 128, 0, 0, 34];
  output = wire.put_be(output, info.min_block as u64, 2);
  output = wire.put_be(output, info.max_block as u64, 2);
  output = wire.put_be(output, 0u64, 6);
  let packed = info.format.sample_rate as u64 << 44u64 | (info.format.channels - 1) as u64 << 41u64
    | (info.format.bits_per_sample - 1) as u64 << 36u64 | info.frames;
  output = wire.put_be(output, packed, 8);
  for byte in info.md5 {
    output = output.append(byte);
  }
  return Ok(output);
}

function number(value: u64): u8[] {
  if (value < 128u64) {
    return [value as u8];
  }
  let count = 2;
  while (count < 7 && value >= 1u64 << (5 * count + 1) as u64) {
    count += 1;
  }
  let first = (255u32 << (8 - count) as u32) as u8 | (value >> (6 * (count - 1)) as u64) as u8;
  let output: u8[] = [first];
  for i in 1..count {
    output = output.append(128u8 | (value >> (6 * (count - 1 - i)) as u64) as u8 & 63u8);
  }
  return output;
}

function block_code(frames: i32): i32 {
  if (frames == 192) {
    return 1;
  }
  for code in 2..6 {
    if (frames == 576 << code - 2) {
      return code;
    }
  }
  for code in 8..16 {
    if (frames == 256 << code - 8) {
      return code;
    }
  }
  return if (frames <= 256) { 6 } else { 7 };
}

function frame_header(frames: i32, assignment: i32, number_value: u64, variable: boolean): u8[] {
  let code = block_code(frames);
  let output: u8[] = [255, if (variable) { 249u8 } else { 248u8 }, (code << 4) as u8, (assignment << 4) as u8];
  for byte in number(number_value) {
    output = output.append(byte);
  }
  if (code == 6 || code == 7) {
    output = wire.put_be(output, (frames - 1) as u64, if (code == 6) { 1 } else { 2 });
  }
  return output.append(checksum.crc(output, false) as u8);
}

function select(input: [i32], format: pcm.Format, options: Options): Result[Selection, pcm.AudioError] {
  let assignments = if (format.channels == 2 && options.stereo) { [1, 8, 9, 10] } else { [format.channels - 1] };
  let selected = Selection { assignment: format.channels - 1, channels: [], bits: 9223372036854775807i64 };
  let scratch: u32[] = [];
  for assignment in assignments {
    let channels: plan.Channel[] = [];
    let bits = 0i64;
    for channel in 0..format.channels {
      let (chosen, next) = plan.choose(input, format.channels, channel, assignment, format.bits_per_sample, options.max_fixed_order, options.max_partition_order, scratch)?;
      scratch = next;
      bits += chosen.bit_count();
      channels = channels.append(chosen);
    }
    if (bits < selected.bits) {
      selected = Selection { assignment: assignment, channels: channels, bits: bits };
    }
  }
  return Ok(selected);
}

function position(value: Position): Result[(u64, boolean), pcm.AudioError] {
  match (value) {
    FrameNumber(n) => {
      if (n > 2147483647u32) {
        return Err(pcm.Header);
      }
      return Ok((n as u64, false));
    },
    SampleNumber(n) => {
      if (n > 68719476735u64) {
        return Err(pcm.Header);
      }
      return Ok((n, true));
    }
  }
}

function input_frames(input: [i32], format: pcm.Format, options: Options, limits: pcm.Limits): Result[i32, pcm.AudioError] {
  pcm.check_input([], limits)?;
  if (!valid_format(format) || !valid_options(options)) {
    return Err(pcm.Header);
  }
  let frames = pcm.validate(input, format, limits)?;
  if (frames < 1) {
    return Err(pcm.Header);
  }
  return Ok(frames);
}

function numbered_header(frames: i32, assignment: i32, at: Position): Result[(u8[], u64, boolean), pcm.AudioError] {
  let (number_value, variable) = position(at)?;
  if (variable && frames as u64 > 68719476735u64 - number_value) {
    return Err(pcm.Header);
  }
  return Ok((frame_header(frames, assignment, number_value, variable), number_value, variable));
}

pub function encode_frame(input: [i32], format: pcm.Format, at: Position, options: Options, limits: pcm.Limits): Result[EncodedFrame, pcm.AudioError] {
  let frames = input_frames(input, format, options, limits)?;
  if (frames > options.block_frames) {
    return Err(pcm.Limit);
  }
  let selected = select(input, format, options)?;
  let (header, number_value, variable) = numbered_header(frames, selected.assignment, at)?;
  let bytes = header.len() as i64 + (selected.bits + 7i64) / 8i64 + 2i64;
  if (bytes > limits.max_input_bytes as i64) {
    return Err(pcm.Limit);
  }
  let output = bitwriter.create(bytes as i32, header)?;
  for channel in selected.channels {
    output = channel.write(input, output)?;
  }
  return Ok(EncodedFrame { frames: frames, number: number_value, variable: variable, assignment: selected.assignment, bytes: output.finish()? });
}

pub function encode(input: [i32], format: pcm.Format, options: Options, limits: pcm.Limits): Result[u8[], pcm.AudioError] {
  let frames = input_frames(input, format, options, limits)?;
  let digest: u8[] = [];
  for _ in 0..16 {
    digest = digest.append(0u8);
  }
  let output = stream_header(StreamInfo { format: format, frames: frames as u64, min_block: options.block_frames, max_block: options.block_frames, md5: digest }, limits)?;
  let hash = crypto.md5_new();
  let at = 0;
  let index = 0u32;
  while (at < frames) {
    let count = if (frames - at < options.block_frames) { frames - at } else { options.block_frames };
    let samples = input[at * format.channels:(at + count) * format.channels];
    let frame = encode_frame(samples, format, FrameNumber(index), options, pcm.Limits { ...limits, max_input_bytes: limits.max_input_bytes - output.len() })?;
    for byte in frame.bytes {
      output = output.append(byte);
    }
    let raw: u8[] = [];
    for value in samples {
      raw = wire.put_le(raw, value as u64, (format.bits_per_sample + 7) / 8);
    }
    hash = hash.update(raw);
    at += count;
    index += 1u32;
  }
  let complete = hash.finish();
  for i in 0..16 {
    output = output.with(26 + i, complete[i]);
  }
  return Ok(output);
}
```
