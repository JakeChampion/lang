# Indexed MP3 file playback

This module builds a validated MP3 frame index from a borrowed seekable source.
It decodes one frame at a time during construction, without retaining the complete input or PCM.
The index uses 64-bit file offsets and logical 16-byte frame entries.
Array capacity, headers, and allocator overhead add to that storage.

Trailer inspection uses at most 160 final bytes and an optional 32-byte APE header.
It excludes trailing tags before scanning audio, including APE payloads that resemble complete MP3 frames.
Leading ID3 payloads pass through a bounded window.
The metadata budget covers leading and trailing tags together.

Playback restores up to 511 reservoir bytes before two complete frames warm the filters.
Recognized Xing/LAME delay and padding apply once.
Each cooperative step performs at most one seek, one bounded read, or one frame decode.
The caller supplies the same immutable source, preserves its position during pending reads, and closes it.

```fern
<<*>>=
// Index and seek MP3 files without retaining a complete encoded input view.

import "std/audio/file";
import "std/audio/mp3";
import "std/audio/mp3/header";
import "std/audio/mp3/input";
import "std/audio/mp3/metadata";
import "std/audio/mp3/seek";
import "std/audio/pcm";
import "std/audio/source";

pub struct Limits { input: input.Limits, max_entries: i32 }

pub struct Entry { offset: i64, bytes: i32, payload: i32 }

pub struct Index { info: input.Info, entries: Entry[], frame_size: i32, frames: i64, input_bytes: i64, limits: Limits }

enum Stage { Tail, ApeHeader, Start, Decode, Complete }

pub struct Builder { decoder: input.Input, entries: Entry[], frame_size: i32, window: Option[file.Window], trailer: metadata.Trailer, audio_end: i64, input_bytes: i64, stage: Stage, limits: Limits }

pub enum BuildEvent { BuildPending, BuildReady(Index), BuildFinished }

pub struct Cursor { index: Index, codec: mp3.Decoder, entry: i32, target: i64, window: Option[file.Window] }

pub enum ReadEvent { Pending, Output(seek.Audio), Finished }

pub function default_limits(): Limits {
  return Limits { input: input.default_limits(), max_entries: 1048576 };
}

function audio_error(e: pcm.AudioError): source.ReadError {
  return source.ReadAudio(e);
}

function tag_limits(limits: Limits): source.Limits {
  return source.Limits { chunk_size: limits.input.input.chunk_size, max_buffer_bytes: 160 };
}

pub function create(input_bytes: i64, limits: Limits): Result[Builder, source.ReadError] {
  if (limits.max_entries < 1) {
    return Err(audio_error(pcm.Limit));
  }
  if (input_bytes < 4i64) {
    return Err(audio_error(pcm.Truncated));
  }
  let decoder = input.create(limits.input)?;
  let count = if (input_bytes < 160i64) { input_bytes as i32 } else { 160 };
  let window = file.create(input_bytes - count as i64, count, tag_limits(limits))?;
  let trailer = match (metadata.trailer(limits.input.max_metadata_bytes)) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  return Ok(Builder { decoder: decoder, entries: [], frame_size: 0, window: Some(window), trailer: trailer, audio_end: input_bytes, input_bytes: input_bytes, stage: Tail, limits: limits });
}

function bytes(input: [u8]): u8[] {
  let output: u8[] = [];
  for byte in input {
    output = output.append(byte);
  }
  return output;
}

function finish_tail(own builder: Builder): Result[(Builder, BuildEvent), source.ReadError] {
  if let Err(e) = builder.trailer.finish() {
    return Err(audio_error(e));
  }
  let limits = input.Limits { ...builder.decoder.limits, max_metadata_bytes: builder.limits.input.max_metadata_bytes - builder.trailer.bytes };
  let decoder = input.Input { ...builder.decoder, limits: limits };
  return Ok((Builder { ...builder, decoder: decoder, window: None, stage: Start }, BuildPending));
}

function inspect_tail(own builder: Builder, window: file.Window): Result[(Builder, BuildEvent), source.ReadError] {
  let tail = match (metadata.inspect_tail(window.view(), builder.input_bytes)) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  if (tail.bytes > builder.limits.input.max_metadata_bytes) {
    return Err(audio_error(pcm.Limit));
  }
  let audio_end = builder.input_bytes - tail.bytes;
  let trailer = metadata.Trailer { first: [], last: bytes(window.view()), bytes: tail.bytes, limit: builder.limits.input.max_metadata_bytes };
  let next = Builder { ...builder, trailer: trailer, audio_end: audio_end, window: None };
  if (!tail.header) {
    return finish_tail(next);
  }
  let header = file.create(audio_end, 32, tag_limits(next.limits))?;
  return Ok((Builder { ...next, window: Some(header), stage: ApeHeader }, BuildPending));
}

function read_tail(own builder: Builder, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Builder, BuildEvent), source.ReadError] {
  let Builder { decoder, entries, frame_size, window, trailer, audio_end, input_bytes, stage, limits } = builder;
  let Some(current) = window else {
    return Err(audio_error(pcm.Header));
  };
  if (!current.ready) {
    let next = current.step_with(seek, read)?;
    return Ok((Builder { decoder: decoder, entries: entries, frame_size: frame_size, window: Some(next), trailer: trailer, audio_end: audio_end, input_bytes: input_bytes, stage: stage, limits: limits }, BuildPending));
  }
  let remainder = Builder { decoder: decoder, entries: entries, frame_size: frame_size, window: None, trailer: trailer, audio_end: audio_end, input_bytes: input_bytes, stage: stage, limits: limits };
  if let Tail = stage {
    return inspect_tail(remainder, current);
  }
  let next_trailer = metadata.Trailer { ...remainder.trailer, first: bytes(current.view()) };
  return finish_tail(Builder { ...remainder, trailer: next_trailer });
}

function record(own builder: Builder, offset: i64, encoded: i64, h: Option[header.Header]): Result[Builder, source.ReadError] {
  if (builder.decoder.encoded_frames == encoded) {
    return Ok(builder);
  }
  if (builder.entries.len() >= builder.limits.max_entries) {
    return Err(audio_error(pcm.Limit));
  }
  let Some(h) = h else {
    return Err(audio_error(pcm.Header));
  };
  let payload = h.bytes - 4 - (if (h.crc) { 2 } else { 0 }) - header.side_bytes(h);
  if (payload < 0 || builder.decoder.offset - offset != h.bytes as i64) {
    return Err(audio_error(pcm.Header));
  }
  let entry = Entry { offset: offset, bytes: h.bytes, payload: payload };
  return Ok(Builder { ...builder, entries: builder.entries.append(entry), frame_size: h.frames });
}

function peek(decoder: input.Input): Option[header.Header] {
  return match (header.parse(decoder.buffer.view(), 0)) { Ok(value) => Some(value), Err(_) => None };
}

function build_event(own builder: Builder, own value: input.ReadEvent): Result[(Builder, BuildEvent), source.ReadError] {
  match (value) {
    input.Output(input.End(summary)) => {
      if (summary.bytes_read != builder.audio_end) {
        return Err(audio_error(pcm.Header));
      }
      let index = Index { info: summary.info, entries: builder.entries, frame_size: builder.frame_size, frames: summary.frames, input_bytes: builder.input_bytes, limits: builder.limits };
      return Ok((Builder { ...builder, entries: [], stage: Complete }, BuildReady(index)));
    },
    input.Finished => {
      return Err(audio_error(pcm.Truncated));
    },
    _ => {
      return Ok((builder, BuildPending));
    }
  }
}

function decode_step(own builder: Builder, read: (i32) => Result[u8[], IoError]): Result[(Builder, BuildEvent), source.ReadError] {
  let offset = builder.decoder.offset;
  let encoded = builder.decoder.encoded_frames;
  let h = peek(builder.decoder);
  let left = builder.audio_end - offset - builder.decoder.buffer.available() as i64;
  if (left < 0i64) {
    return Err(source.ReadSize);
  }
  let bounded = (count: i32): Result[u8[], IoError] => {
    if (left == 0i64) {
      return Ok([]);
    }
    return read(if (left < count as i64) { left as i32 } else { count });
  };
  let (decoder, value) = builder.decoder.step_with(bounded)?;
  if (decoder.offset > builder.audio_end - decoder.buffer.available() as i64) {
    return Err(source.ReadSize);
  }
  let next = record(Builder { ...builder, decoder: decoder }, offset, encoded, h)?;
  return build_event(next, value);
}

pub function (own builder: Builder) step_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Builder, BuildEvent), source.ReadError] {
  match (builder.stage) {
    Tail => {
      return read_tail(builder, seek, read);
    },
    ApeHeader => {
      return read_tail(builder, seek, read);
    },
    Start => {
      let offset = match (seek(0i64)) { Ok(value) => value, Err(e) => { return Err(source.ReadIo(e)); } };
      if (offset != 0i64) {
        return Err(source.ReadSize);
      }
      return Ok((Builder { ...builder, stage: Decode }, BuildPending));
    },
    Decode => {
      return decode_step(builder, read);
    },
    Complete => {
      return Ok((builder, BuildFinished));
    }
  }
}

pub function (own builder: Builder) step(reader: Reader): Result[(Builder, BuildEvent), source.ReadError] {
  return builder.step_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}

pub function inspect_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], input_bytes: i64, limits: Limits): Result[Index, source.ReadError] {
  let builder = create(input_bytes, limits)?;
  while (true) {
    let (next, value) = builder.step_with(seek, read)?;
    builder = next;
    match (value) {
      BuildReady(index) => {
        return Ok(index);
      },
      BuildFinished => {
        return Err(audio_error(pcm.Header));
      },
      BuildPending => {}
    }
  }
}

pub function inspect(reader: Reader, limits: Limits): Result[Index, source.ReadError] {
  let size = match (reader.seek(0i64, 2)) { Ok(value) => value, Err(e) => { return Err(source.ReadIo(e)); } };
  return inspect_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), size, limits);
}

pub function reader(own index: Index): Cursor {
  let target = index.info.skip_frames as i64;
  return Cursor { index: index, codec: mp3.decoder(), entry: 0, target: target, window: None };
}

function warmup(index: Index, entry: i32): i32 {
  let start = if (entry > 2) { entry - 2 } else { 0 };
  let bytes = 0;
  while (start > 0 && bytes < 511) {
    start -= 1;
    bytes += index.entries[start].payload;
  }
  return start;
}

pub function (own cursor: Cursor) seek(position: i64): Result[Cursor, source.ReadError] {
  if (position < 0i64 || position > cursor.index.frames) {
    return Err(audio_error(pcm.Header));
  }
  let Some(target) = position +? cursor.index.info.skip_frames as i64 else {
    return Err(audio_error(pcm.Limit));
  };
  let entry = if (position == cursor.index.frames) { cursor.index.entries.len() } else { warmup(cursor.index, (target / (cursor.index.frame_size as i64)) as i32) };
  return Ok(Cursor { ...cursor, codec: cursor.codec.reset(), entry: entry, target: target, window: None });
}

function crop(samples: [f32], start: i32, end: i32): f32[] {
  let output: f32[] = [];
  for i in start..end {
    output = output.append(samples[i]);
  }
  return output;
}

function output(own frame: mp3.Frame, begin: i64, start: i64, end: i64, skip: i64): seek.Audio {
  let first = (start - begin) as i32 * frame.channels;
  let last = (end - begin) as i32 * frame.channels;
  let samples = if (first == 0 && last == frame.samples.len()) { frame.samples } else { crop(frame.samples, first, last) };
  return seek.Audio { sample_rate: frame.sample_rate, channels: frame.channels, position: start - skip, frames: (end - start) as i32, samples: samples };
}

function frame_matches(frame: mp3.Frame, index: Index, entry: i32): boolean {
  return frame.sample_rate == index.info.sample_rate && frame.channels == index.info.channels && frame.bytes_read == index.entries[entry].bytes;
}

function decode(own index: Index, own codec: mp3.Decoder, entry: i32, target: i64, own window: file.Window): Result[(Cursor, ReadEvent), source.ReadError] {
  let (next_codec, result) = codec.decode_frame(window.view(), 0, index.limits.input.audio);
  let frame = match (result) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  let begin = entry as i64 * (index.frame_size as i64);
  let limit = index.info.skip_frames as i64 + index.frames;
  let end = if (begin + index.frame_size as i64 > limit) { limit } else { begin + index.frame_size as i64 };
  let start = if (begin < target) { target } else { begin };
  if (!frame_matches(frame, index, entry)) {
    return Err(audio_error(pcm.Header));
  }
  let next = Cursor { index: index, codec: next_codec, entry: entry + 1, target: target, window: None };
  if (start >= end) {
    return Ok((next, Pending));
  }
  if (frame.needs_history || frame.frames != next.index.frame_size) {
    return Err(audio_error(pcm.Header));
  }
  let audio = output(frame, begin, start, end, next.index.info.skip_frames as i64);
  return Ok((next, Output(audio)));
}

pub function (own cursor: Cursor) step_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Cursor, ReadEvent), source.ReadError] {
  let Cursor { index, codec, entry, target, window } = cursor;
  let count = index.entries.len();
  if (entry == count || entry as i64 * (index.frame_size as i64) >= index.info.skip_frames as i64 + index.frames) {
    return Ok((Cursor { index: index, codec: codec, entry: count, target: target, window: None }, Finished));
  }
  let current = match (window) { Some(value) => value, None => file.create(index.entries[entry].offset, index.entries[entry].bytes, index.limits.input.input)? };
  if (current.ready) {
    return decode(index, codec, entry, target, current);
  }
  let next = current.step_with(seek, read)?;
  return Ok((Cursor { index: index, codec: codec, entry: entry, target: target, window: Some(next) }, Pending));
}

pub function (own cursor: Cursor) step(reader: Reader): Result[(Cursor, ReadEvent), source.ReadError] {
  return cursor.step_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}

pub function (own cursor: Cursor) next_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Cursor, Option[seek.Audio]), source.ReadError] {
  while (true) {
    let (next, value) = cursor.step_with(seek, read)?;
    cursor = next;
    match (value) {
      Output(audio) => {
        return Ok((cursor, Some(audio)));
      },
      Finished => {
        return Ok((cursor, None));
      },
      Pending => {}
    }
  }
}

pub function (own cursor: Cursor) next(reader: Reader): Result[(Cursor, Option[seek.Audio]), source.ReadError] {
  return cursor.next_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
