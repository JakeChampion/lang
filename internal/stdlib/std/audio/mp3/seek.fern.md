# MP3 seek indexes and bounded output

This module builds a frame index for indexed-bitrate and free-format Layer III tracks.
The index stores byte offsets. It does not retain encoded bytes or decoded samples.
The caller supplies the same immutable encoded input to each reader call.
Input and index limits apply before the corresponding allocation.

Each `next` call returns at most one frame of playable float PCM.
The reader applies recognized Xing/LAME delay and padding once.
`seek` accepts a playable frame position, including the exact end of the track.
It resets the codec, restores reservoir history, and warms the overlap and synthesis filters before output.
Positions count frames per channel. They use the track sample rate.

The index holds one `i32` offset per encoded audio frame, plus array and track metadata.
Array capacity and allocator overhead add to the logical four bytes per entry.
The reader retains codec history and at most one frame of decoded output.
These calls borrow a complete input view. They do not read a file or scan damaged input for synchronization.

```fern
<<*>>=
// Seek MP3 with reservoir and synthesis warmup.

import "std/audio/mp3";
import "std/audio/mp3/header";
import "std/audio/mp3/framing";
import "std/audio/pcm";

pub struct Limits { audio: pcm.Limits, max_entries: i32 }

pub struct Index { track: mp3.Track, offsets: i32[], frame_size: i32, frames: i64, input_bytes: i32, limits: Limits }

pub struct Cursor { index: Index, codec: mp3.Decoder, entry: i32, target: i64 }

pub struct Audio { sample_rate: i32, channels: i32, position: i64, frames: i32, samples: f32[] }

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), max_entries: 1048576 };
}

function frame_matches(h: header.Header, track: mp3.Track, size: i32): boolean {
  return h.sample_rate == track.sample_rate && h.channels == track.channels && h.frames == size;
}

function total(track: mp3.Track, count: i32, size: i32): Result[i64, pcm.AudioError] {
  let raw_frames = count as i64 * (size as i64);
  let removed = track.skip_frames as i64 + track.padding_frames as i64;
  if (count == 0 || removed > raw_frames) {
    return Err(pcm.Header);
  }
  if (track.declared_frames != 0u32 && track.declared_frames != count as u32) {
    return Err(pcm.Header);
  }
  return Ok(raw_frames - removed);
}

pub function inspect(input: [u8], limits: Limits): Result[Index, pcm.AudioError] {
  if (limits.max_entries < 1) {
    return Err(pcm.Limit);
  }
  let track = mp3.inspect_track(input, limits.audio)?;
  let first = mp3.inspect_cached(input, track.data_offset, track.free_bytes, limits.audio)?;
  let offsets: i32[] = [];
  let at = track.data_offset;
  let free_bytes = track.free_bytes;
  while (at < track.data_end) {
    let h = mp3.inspect_cached(input, at, free_bytes, limits.audio)?;
    if (!frame_matches(h, track, first.frames)) {
      return Err(pcm.Unsupported);
    }
    if (h.bytes > track.data_end - at) {
      return Err(pcm.Truncated);
    }
    if (offsets.len() >= limits.max_entries) {
      return Err(pcm.Limit);
    }
    offsets = offsets.append(at);
    free_bytes = framing.extent(h, free_bytes);
    at += h.bytes;
  }
  let frames = total(track, offsets.len(), first.frames)?;
  return Ok(Index { track: mp3.Track { ...track, free_bytes: free_bytes }, offsets: offsets, frame_size: first.frames, frames: frames, input_bytes: input.len(), limits: limits });
}

pub function reader(own index: Index): Cursor {
  let target = index.track.skip_frames as i64;
  let codec = mp3.decoder();
  if (index.track.free_bytes > 0) {
    codec = mp3.Decoder { ...codec, free_bytes: index.track.free_bytes };
  }
  return Cursor { index: index, codec: codec, entry: 0, target: target };
}

function warmup(input: [u8], index: Index, entry: i32): Result[i32, pcm.AudioError] {
  // Two complete frames settle the hybrid overlap and synthesis history.
  // Before those frames, restore the maximum Layer III reservoir of 511 bytes.
  let start = if (entry > 2) { entry - 2 } else { 0 };
  let bytes = 0;
  while (start > 0 && bytes < 511) {
    start -= 1;
    let h = mp3.inspect_cached(input, index.offsets[start], index.track.free_bytes, index.limits.audio)?;
    let payload = h.bytes - 4 - (if (h.crc) { 2 } else { 0 }) - header.side_bytes(h);
    if (payload < 0) {
      return Err(pcm.Header);
    }
    bytes += payload;
  }
  return Ok(start);
}

pub function (own r: Cursor) seek(input: [u8], position: i64): Result[Cursor, pcm.AudioError] {
  if (input.len() != r.index.input_bytes || position < 0i64 || position > r.index.frames) {
    return Err(pcm.Header);
  }
  let target = position + r.index.track.skip_frames as i64;
  let entry = if (position == r.index.frames) { r.index.offsets.len() } else { warmup(input, r.index, (target / (r.index.frame_size as i64)) as i32)? };
  let codec = r.codec.reset();
  if (r.index.track.free_bytes > 0) {
    codec = mp3.Decoder { ...codec, free_bytes: r.index.track.free_bytes };
  }
  return Ok(Cursor { index: r.index, codec: codec, entry: entry, target: target });
}

function crop(samples: [f32], start: i32, end: i32): f32[] {
  let output: f32[] = [];
  for i in start..end {
    output = output.append(samples[i]);
  }
  return output;
}

function output(own frame: mp3.Frame, begin: i64, start: i64, end: i64, skip: i64): Audio {
  let first = (start - begin) as i32 * frame.channels;
  let last = (end - begin) as i32 * frame.channels;
  let samples = if (first == 0 && last == frame.samples.len()) { frame.samples } else { crop(frame.samples, first, last) };
  return Audio { sample_rate: frame.sample_rate, channels: frame.channels, position: start - skip, frames: (end - start) as i32, samples: samples };
}

pub function (own r: Cursor) next(input: [u8]): Result[(Cursor, Option[Audio]), pcm.AudioError] {
  let Cursor { index, codec, entry, target } = r;
  if (input.len() != index.input_bytes) {
    return Err(pcm.Header);
  }
  let limit = index.track.skip_frames as i64 + index.frames;
  while (entry < index.offsets.len()) {
    let begin = entry as i64 * (index.frame_size as i64);
    if (begin >= limit) {
      break;
    }
    let (next_codec, result) = codec.decode_frame(input, index.offsets[entry], index.limits.audio);
    codec = next_codec;
    let frame = result?;
    entry += 1;
    let end = if (begin + index.frame_size as i64 > limit) { limit } else { begin + index.frame_size as i64 };
    let start = if (begin < target) { target } else { begin };
    if (start < end) {
      if (frame.needs_history || frame.frames != index.frame_size) {
        return Err(pcm.Header);
      }
      let audio = output(frame, begin, start, end, index.track.skip_frames as i64);
      return Ok((Cursor { index: index, codec: codec, entry: entry, target: target }, Some(audio)));
    }
  }
  return Ok((Cursor { index: index, codec: codec, entry: index.offsets.len(), target: target }, None));
}
```
