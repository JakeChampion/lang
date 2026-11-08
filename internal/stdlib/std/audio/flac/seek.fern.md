# FLAC frame indexes and bounded output

This module indexes native FLAC frames and reads playable sample ranges.
The caller supplies the same immutable encoded input to each call.
Each `next` call returns at most one frame of integer PCM.
Positions count samples per channel, starting at zero.

Index construction decodes one frame at a time to validate its length, checksums, and sequence.
It retains byte offsets and sample positions, without retaining encoded data or PCM.
It does not verify the whole-stream MD5. Use complete-stream decode for that check.
The sample limit applies to each frame, so a track can exceed that output limit.
Input bytes and index entries have separate limits.

The logical index data uses one `i32` offset and one `i64` position per frame.
Array capacity, headers, and allocator overhead add to those twelve bytes per entry.
The cursor retains no decoded history. A FLAC frame supplies all state needed after a seek.
These calls borrow a complete input view and do not perform file I/O.

```fern
<<*>>=
// Index native FLAC frames and decode bounded sample ranges after a seek.

import "std/audio/flac";
import "std/audio/pcm";

pub struct Limits { audio: pcm.Limits, max_entries: i32 }

pub struct Index { stream: flac.Header, offsets: i32[], positions: i64[], frames: i64, input_bytes: i32, limits: Limits }

pub struct Cursor { index: Index, entry: i32, target: i64 }

pub struct Audio { format: pcm.Format, position: i64, frames: i32, samples: i32[] }

struct Chain { frames: i64, index: i32, variable: boolean, block_size: i32, previous: i32 }

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), max_entries: 1048576 };
}

function follows(chain: Chain, frame: flac.Frame): boolean {
  return frame.variable == chain.variable && (chain.variable || chain.previous == chain.block_size && frame.frames <= chain.block_size);
}

function advance(own chain: Chain, frame: flac.Frame): Result[Chain, pcm.AudioError] {
  if (chain.index == 0) {
    chain = Chain { ...chain, variable: frame.variable, block_size: frame.frames };
  } else if (!follows(chain, frame)) {
    return Err(pcm.Header);
  }
  let expected = if (chain.variable) { chain.frames as u64 } else { chain.index as u64 };
  if (frame.number != expected) {
    return Err(pcm.Header);
  }
  return Ok(Chain { ...chain, frames: chain.frames + frame.frames as i64, index: chain.index + 1, previous: frame.frames });
}

function complete(chain: Chain, stream: flac.Header): boolean {
  return chain.index > 0 && (stream.frames == 0u64 || chain.frames as u64 == stream.frames);
}

pub function inspect(input: [u8], limits: Limits): Result[Index, pcm.AudioError] {
  if (limits.max_entries < 1) {
    return Err(pcm.Limit);
  }
  let stream = flac.inspect(input, limits.audio)?;
  let count = pcm.sample_count(stream.max_block, stream.format.channels, limits.audio)?;
  let scratch = pcm.zeros(count);
  let offsets: i32[] = [];
  let positions: i64[] = [];
  let chain = Chain { frames: 0i64, index: 0, variable: false, block_size: 0, previous: 0 };
  let at = stream.data_offset;
  while (at < input.len()) {
    if (chain.index >= limits.max_entries) {
      return Err(pcm.Limit);
    }
    let frame = flac.decode_frame_into(input, at, stream, scratch, limits.audio)?;
    offsets = offsets.append(at);
    positions = positions.append(chain.frames);
    chain = advance(chain, frame)?;
    at += frame.bytes_read;
    scratch = frame.samples;
  }
  if (!complete(chain, stream)) {
    return Err(pcm.Truncated);
  }
  return Ok(Index { stream: stream, offsets: offsets, positions: positions, frames: chain.frames, input_bytes: input.len(), limits: limits });
}

pub function reader(own index: Index): Cursor {
  return Cursor { index: index, entry: 0, target: 0i64 };
}

function entry_at(positions: [i64], position: i64): i32 {
  let low = 0;
  let high = positions.len();
  while (low < high) {
    let middle = low + (high - low) / 2;
    if (positions[middle] <= position) {
      low = middle + 1;
    } else {
      high = middle;
    }
  }
  return low - 1;
}

pub function (own cursor: Cursor) seek(position: i64): Result[Cursor, pcm.AudioError] {
  if (position < 0i64 || position > cursor.index.frames) {
    return Err(pcm.Header);
  }
  let entry = if (position == cursor.index.frames) { cursor.index.offsets.len() } else { entry_at(cursor.index.positions, position) };
  return Ok(Cursor { index: cursor.index, entry: entry, target: position });
}

function samples(own frame: flac.Frame, start: i32): i32[] {
  if (start == 0) {
    return frame.samples;
  }
  let output: i32[] = [];
  for i in start * frame.format.channels..frame.frames * frame.format.channels {
    output = output.append(frame.samples[i]);
  }
  return output;
}

pub function (own cursor: Cursor) next(input: [u8]): Result[(Cursor, Option[Audio]), pcm.AudioError] {
  if (input.len() != cursor.index.input_bytes) {
    return Err(pcm.Header);
  }
  if (cursor.entry == cursor.index.offsets.len()) {
    return Ok((cursor, None));
  }
  let frame = flac.decode_frame(input, cursor.index.offsets[cursor.entry], cursor.index.stream, cursor.index.limits.audio)?;
  let begin = cursor.index.positions[cursor.entry];
  let start = if (cursor.target > begin) { (cursor.target - begin) as i32 } else { 0 };
  let format = frame.format;
  let count = frame.frames - start;
  let audio = Audio { format: format, position: begin + start as i64, frames: count, samples: samples(frame, start) };
  return Ok((Cursor { ...cursor, entry: cursor.entry + 1 }, Some(audio)));
}
```
