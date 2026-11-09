# Indexed WavPack file playback

This module validates a seekable source and records one entry per audio group.
Index construction retains one encoded group and reusable PCM scratch storage.
It validates every group checksum and all trailing tags before it returns the index.
The index retains offsets and sample positions, without encoded input or PCM.
Each logical entry uses 24 bytes, plus array capacity and allocator overhead.

Playback reads only the indexed group that contains the requested sample.
It validates the complete group before it copies the requested suffix into caller storage.
Positions count samples per channel.
The caller owns the source and must supply the same immutable file throughout index construction and playback.

Each step performs at most one seek, one bounded read, or one group decode.
Every progress result returns caller storage.
Only an `Output` event declares a valid sample prefix.
An end-of-track seek completes without source operations.

```fern
<<*>>=
// Index source ranges and decode exact WavPack sample positions.
import "std/audio/file";
import "std/audio/wavpack";
import "std/audio/wavpack/block";
import "std/audio/wavpack/input";
import "std/audio/source";
import "std/audio/pcm";

pub struct Limits { input: input.Limits, max_entries: i32 }

pub struct Entry { offset: i64, position: i64, bytes: i32, frames: i32 }

pub opaque struct Index { info: wavpack.Info, entries: Entry[], bytes: i64, limits: Limits }

pub opaque struct Builder { decoder: Option[input.Input], entries: Entry[], samples: i32[], started: boolean, done: boolean, limits: Limits }

pub enum BuildEvent { BuildPending, BuildReady(Index), BuildFinished }

pub opaque struct Cursor { index: Index, entry: i32, target: i64, window: Option[file.Window], scratch: i32[] }

pub enum ReadEvent { Pending, Output(input.Audio), Finished }

pub struct Progress { state: Cursor, event: ReadEvent, samples: i32[] }

pub function default_limits(): Limits {
  return Limits { input: input.default_limits(), max_entries: 1048576 };
}

function error(value: pcm.AudioError): source.ReadError {
  return source.ReadAudio(value);
}

pub function create(limits: Limits): Result[Builder, source.ReadError] {
  if (limits.max_entries < 1) {
    return Err(error(pcm.Limit));
  }
  return Ok(Builder { decoder: Some(input.create(limits.input)?), entries: [], samples: [], started: false, done: false, limits: limits });
}

function build_event(own builder: Builder, value: input.ReadEvent): Result[(Builder, BuildEvent), source.ReadError] {
  return match (value) {
    input.Output(input.Samples(audio)) => {
      if (builder.entries.len() >= builder.limits.max_entries) {
        return Err(error(pcm.Limit));
      }
      let entry = Entry { offset: audio.offset, position: audio.position, bytes: audio.bytes, frames: audio.frames };
      return Ok((Builder { ...builder, entries: builder.entries.append(entry) }, BuildPending));
    },
    input.Output(input.End(summary)) => {
      let index = Index { info: summary.info, entries: builder.entries, bytes: summary.bytes_read, limits: builder.limits };
      return Ok((Builder { decoder: None, entries: [], samples: [], started: true, done: true, limits: builder.limits }, BuildReady(index)));
    },
    input.Finished => Err(error(pcm.Truncated)),
    _ => Ok((builder, BuildPending)),
  };
}

pub function (own builder: Builder) step_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Builder, BuildEvent), source.ReadError] {
  if (builder.done) {
    return Ok((builder, BuildFinished));
  }
  if (!builder.started) {
    let at = match (seek(0i64)) { Ok(value) => value, Err(e) => { return Err(source.ReadIo(e)); } };
    if (at != 0i64) {
      return Err(source.ReadSize);
    }
    return Ok((Builder { ...builder, started: true }, BuildPending));
  }
  let Builder { decoder: current, entries, samples, started, done, limits } = builder;
  let Some(decoder) = current else {
    return Err(error(pcm.Header));
  };
  let input.Progress { state, event, samples: output } = decoder.step_into_with(read, samples)?;
  return build_event(Builder { decoder: Some(state), entries: entries, samples: output, started: started, done: done, limits: limits }, event);
}

pub function (own builder: Builder) step(reader: Reader): Result[(Builder, BuildEvent), source.ReadError] {
  return builder.step_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}

pub function inspect_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], limits: Limits): Result[Index, source.ReadError] {
  let builder = create(limits)?;
  while (true) {
    let (next, event) = builder.step_with(seek, read)?;
    builder = next;
    match (event) {
      BuildReady(index) => {
        return Ok(index);
      },
      BuildFinished => {
        return Err(error(pcm.Header));
      },
      BuildPending => {}
    }
  }
}

pub function inspect(reader: Reader, limits: Limits): Result[Index, source.ReadError] {
  return inspect_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), limits);
}

pub function (index: Index) information(): wavpack.Info {
  return index.info;
}

pub function (index: Index) entries(): [Entry] {
  return index.entries[:];
}

pub function (index: Index) input_bytes(): i64 {
  return index.bytes;
}

pub function reader(index: Index): Cursor {
  return Cursor { index: index, entry: 0, target: 0i64, window: None, scratch: [] };
}

function entry_at(entries: [Entry], position: i64): i32 {
  let low = 0;
  let high = entries.len();
  while (low < high) {
    let middle = low + (high - low) / 2;
    if (entries[middle].position <= position) {
      low = middle + 1;
    } else {
      high = middle;
    }
  }
  return low - 1;
}

pub function (own cursor: Cursor) seek(position: i64): Result[Cursor, source.ReadError] {
  if (position < 0i64 || position > cursor.index.info.total_frames) {
    return Err(error(pcm.Sample));
  }
  let entry = if (position == cursor.index.info.total_frames) { cursor.index.entries.len() } else { entry_at(cursor.index.entries, position) };
  return Ok(Cursor { ...cursor, entry: entry, target: position, window: None });
}

function ready_group(plan: wavpack.GroupIndex, bytes: i32, expected_bytes: i32, frames: i32): Result[wavpack.GroupIndex, source.ReadError] {
  if (bytes != expected_bytes || plan.frames() != frames) {
    return Err(error(pcm.Header));
  }
  return Ok(plan);
}

function group(view: [u8], index: Index, entry: Entry): Result[wavpack.GroupIndex, source.ReadError] {
  let limits = index.limits.input.codec;
  let builder = match (wavpack.group_builder(Some(index.info), entry.position, limits)) { Ok(value) => value, Err(e) => { return Err(error(e)); } };
  let at = 0;
  while (at < view.len()) {
    let value = match (block.read(view, at, limits.max_metadata, limits.audio)) { Ok(value) => value, Err(e) => { return Err(error(e)); } };
    at = value.end;
    match (builder.push(view, value)) {
      Err(e) => {
        return Err(error(e));
      },
      Ok(wavpack.GroupPending(next)) => {
        builder = next;
      },
      Ok(wavpack.GroupReady(plan)) => {
        return ready_group(plan, at, view.len(), entry.frames);
      }
    }
  }
  return Err(error(pcm.Truncated));
}

function progress(own state: Cursor, event: ReadEvent, own samples: i32[]): Result[Progress, source.ReadError] {
  return Ok(Progress { state: state, event: event, samples: samples });
}

function decode(own cursor: Cursor, window: file.Window, own output: i32[]): Result[Progress, source.ReadError] {
  let Cursor { index, entry, target, window: _, scratch } = cursor;
  let selected = index.entries[entry];
  let plan = group(window.view(), index, selected)?;
  let (reused, frame) = match (plan.decode_into(window.view(), scratch, output)) { Ok(value) => value, Err(e) => { return Err(error(e)); } };
  let start = if (target > selected.position) { (target - selected.position) as i32 } else { 0 };
  if (start < 0 || start >= frame.frames) {
    return Err(error(pcm.Sample));
  }
  let count = (frame.frames - start) * index.info.format.channels;
  let samples = frame.samples;
  if (start > 0) {
    let skip = start * index.info.format.channels;
    for i in 0..count {
      samples = samples.with(i, samples[i + skip]);
    }
  }
  let audio = input.Audio { info: index.info, position: selected.position + start as i64, frames: frame.frames - start, offset: selected.offset, bytes: selected.bytes };
  return progress(Cursor { index: index, entry: entry + 1, target: target, window: None, scratch: reused }, Output(audio), samples);
}

pub function (own cursor: Cursor) step_into_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], own samples: i32[]): Result[Progress, source.ReadError] {
  let Cursor { index, entry, target, window, scratch } = cursor;
  if (entry == index.entries.len()) {
    return progress(Cursor { index: index, entry: entry, target: target, window: None, scratch: scratch }, Finished, samples);
  }
  let selected = index.entries[entry];
  let current = match (window) { Some(value) => value, None => file.create(selected.offset, selected.bytes, index.limits.input.input)? };
  let state = Cursor { index: index, entry: entry, target: target, window: None, scratch: scratch };
  if (current.ready) {
    return decode(state, current, samples);
  }
  return progress(Cursor { ...state, window: Some(current.step_with(seek, read)?) }, Pending, samples);
}

pub function (own cursor: Cursor) step_into(reader: Reader, own samples: i32[]): Result[Progress, source.ReadError] {
  return cursor.step_into_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), samples);
}

pub function (own cursor: Cursor) next_into_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], own samples: i32[]): Result[Progress, source.ReadError] {
  while (true) {
    let Progress { state, event, samples: output } = cursor.step_into_with(seek, read, samples)?;
    if let Pending = event {
      cursor = state;
      samples = output;
    } else {
      return progress(state, event, output);
    }
  }
}

pub function (own cursor: Cursor) next_into(reader: Reader, own samples: i32[]): Result[Progress, source.ReadError] {
  return cursor.next_into_with((offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), samples);
}
```
