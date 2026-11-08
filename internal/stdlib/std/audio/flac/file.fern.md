# Indexed FLAC file playback

This module builds a validated frame index from a borrowed seekable source.
It decodes one frame at a time during index construction, including complete-stream MD5 validation.
It retains frame offsets and sample positions. It does not retain the complete encoded input or PCM.
The logical index uses 24 bytes per frame, plus array capacity and allocator overhead.

Playback seeks directly to an indexed frame and reads only that encoded range.
Positions count samples per channel. File offsets and positions use signed 64-bit counts.
The source must identify the same immutable file for index construction and playback.
The caller retains and closes it on success or failure.

`step` performs at most one seek, one source read, or one frame decode.
`next` continues until PCM or completion. Each PCM event contains at most one frame.
An end-of-track seek completes without source operations.
Index construction has a cooperative step API and a complete `inspect` convenience function.

```fern
<<*>>=
// Index and seek native FLAC files without a complete encoded input view.

import "std/audio/file";
import "std/audio/flac";
import "std/audio/flac/input";
import "std/audio/flac/seek";
import "std/audio/pcm";
import "std/audio/source";

pub struct Limits { input: input.Limits, max_entries: i32 }
pub struct Entry { offset: i64, position: i64, bytes: i32, frames: i32 }
pub struct Index { info: input.Info, entries: Entry[], frames: i64, input_bytes: i64, limits: Limits }
pub struct Builder { decoder: input.Input, entries: Entry[], started: boolean, done: boolean, limits: Limits }
pub enum BuildEvent { BuildPending, BuildReady(Index), BuildFinished }
pub struct Cursor { index: Index, entry: i32, target: i64, window: Option[file.Window] }
pub enum ReadEvent { Pending, Output(seek.Audio), Finished }

pub function default_limits(): Limits { return Limits { input: input.default_limits(), max_entries: 1048576 }; }

function audio_error(e: pcm.AudioError): source.ReadError { return source.ReadAudio(e); }

pub function create(limits: Limits): Result[Builder, source.ReadError] {
  if (limits.max_entries < 1) { return Err(audio_error(pcm.Limit)); }
  let decoder = input.create(limits.input)?;
  return Ok(Builder { decoder: decoder, entries: [], started: false, done: false, limits: limits });
}

function record(own builder: Builder, offset: i64, position: i64, encoded: i64): Result[Builder, source.ReadError] {
  if (builder.decoder.encoded_frames == encoded) { return Ok(builder); }
  if (builder.entries.len() >= builder.limits.max_entries) { return Err(audio_error(pcm.Limit)); }
  let bytes = builder.decoder.offset - offset;
  let frames = builder.decoder.position - position;
  if (bytes < 1i64 || bytes > 2147483647i64 || frames < 1i64 || frames > 65535i64) { return Err(audio_error(pcm.Header)); }
  let entry = Entry { offset: offset, position: position, bytes: bytes as i32, frames: frames as i32 };
  return Ok(Builder { ...builder, entries: builder.entries.append(entry) });
}

function build_event(own builder: Builder, own value: input.ReadEvent): Result[(Builder, BuildEvent), source.ReadError] {
  match (value) {
    input.Output(input.End(summary)) => {
      let index = Index { info: summary.info, entries: builder.entries, frames: summary.frames, input_bytes: summary.bytes_read, limits: builder.limits };
      return Ok((Builder { ...builder, entries: [], done: true }, BuildReady(index)));
    },
    input.Finished => { return Err(audio_error(pcm.Truncated)); },
    _ => { return Ok((builder, BuildPending)); }
  }
}

pub function (own builder: Builder) step_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Builder, BuildEvent), source.ReadError] {
  if (builder.done) { return Ok((builder, BuildFinished)); }
  if (!builder.started) {
    let offset = match (seek(0i64)) { Ok(value) => value, Err(e) => { return Err(source.ReadIo(e)); } };
    if (offset != 0i64) { return Err(source.ReadSize); }
    return Ok((Builder { ...builder, started: true }, BuildPending));
  }
  let offset = builder.decoder.offset;
  let position = builder.decoder.position;
  let encoded = builder.decoder.encoded_frames;
  let (decoder, value) = builder.decoder.step_with(read)?;
  let next = record(Builder { ...builder, decoder: decoder }, offset, position, encoded)?;
  return build_event(next, value);
}

pub function (own builder: Builder) step(reader: Reader): Result[(Builder, BuildEvent), source.ReadError] {
  return builder.step_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}

pub function inspect_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], limits: Limits): Result[Index, source.ReadError] {
  let builder = create(limits)?;
  while (true) {
    let (next, value) = builder.step_with(seek, read)?;
    builder = next;
    match (value) {
      BuildReady(index) => { return Ok(index); },
      BuildFinished => { return Err(audio_error(pcm.Header)); },
      BuildPending => {}
    }
  }
}

pub function inspect(reader: Reader, limits: Limits): Result[Index, source.ReadError] {
  return inspect_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), limits);
}

pub function reader(own index: Index): Cursor { return Cursor { index: index, entry: 0, target: 0i64, window: None }; }

function entry_at(entries: Entry[], position: i64): i32 {
  let low = 0;
  let high = entries.len();
  while (low < high) {
    let middle = low + (high - low) / 2;
    if (entries[middle].position <= position) { low = middle + 1; } else { high = middle; }
  }
  return low - 1;
}

pub function (own cursor: Cursor) seek(position: i64): Result[Cursor, source.ReadError] {
  if (position < 0i64 || position > cursor.index.frames) { return Err(audio_error(pcm.Header)); }
  let entry = if (position == cursor.index.frames) { cursor.index.entries.len() } else { entry_at(cursor.index.entries, position) };
  return Ok(Cursor { ...cursor, entry: entry, target: position, window: None });
}

function crop(own frame: flac.Frame, start: i32): i32[] {
  if (start == 0) { return frame.samples; }
  let samples: i32[] = [];
  for i in start * frame.format.channels..frame.samples.len() { samples = samples.append(frame.samples[i]); }
  return samples;
}

function decode(own index: Index, entry: i32, target: i64, own window: file.Window): Result[(Cursor, ReadEvent), source.ReadError] {
  let info = index.info;
  let header = flac.Header { format: info.format, frames: info.total_frames, min_block: info.min_block, max_block: info.max_block, data_offset: 0, md5: info.md5 };
  let frame = match (flac.decode_frame(window.view(), 0, header, index.limits.input.audio)) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  let selected = index.entries[entry];
  let number = if (frame.variable) { selected.position as u64 } else { entry as u64 };
  if (frame.bytes_read != selected.bytes || frame.frames != selected.frames || frame.number != number) { return Err(audio_error(pcm.Header)); }
  let start = if (target > selected.position) { (target - selected.position) as i32 } else { 0 };
  if (start < 0 || start >= frame.frames) { return Err(audio_error(pcm.Header)); }
  let audio = seek.Audio { format: frame.format, position: selected.position + start as i64, frames: frame.frames - start, samples: crop(frame, start) };
  return Ok((Cursor { index: index, entry: entry + 1, target: target, window: None }, Output(audio)));
}

pub function (own cursor: Cursor) step_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Cursor, ReadEvent), source.ReadError] {
  let Cursor { index, entry, target, window } = cursor;
  if (entry == index.entries.len()) { return Ok((Cursor { index: index, entry: entry, target: target, window: None }, Finished)); }
  let current = match (window) { Some(value) => value, None => file.create(index.entries[entry].offset, index.entries[entry].bytes, index.limits.input.input)? };
  if (current.ready) { return decode(index, entry, target, current); }
  let next = current.step_with(seek, read)?;
  return Ok((Cursor { index: index, entry: entry, target: target, window: Some(next) }, Pending));
}

pub function (own cursor: Cursor) step(reader: Reader): Result[(Cursor, ReadEvent), source.ReadError] {
  return cursor.step_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}

pub function (own cursor: Cursor) next_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Cursor, Option[seek.Audio]), source.ReadError] {
  while (true) {
    let (next, value) = cursor.step_with(seek, read)?;
    cursor = next;
    match (value) {
      Output(audio) => { return Ok((cursor, Some(audio))); },
      Finished => { return Ok((cursor, None)); },
      Pending => {}
    }
  }
}

pub function (own cursor: Cursor) next(reader: Reader): Result[(Cursor, Option[seek.Audio]), source.ReadError] {
  return cursor.next_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
