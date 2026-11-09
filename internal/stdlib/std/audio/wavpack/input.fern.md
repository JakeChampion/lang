# Bounded WavPack input

This module decodes a sequential WavPack source without a complete encoded input view.
It retains one bounded encoded window, one packet scratch array, and reusable caller PCM storage.
Each step performs at most one source read or one group decode.
PCM float samples use exact IEEE 754 bits in the integer output array.

The caller owns and closes the source.
An empty successful read means permanent EOF.
I/O errors retain their own category.
`End` confirms group checksums, contiguous positions, declared sample totals, and complete trailing tags.
Earlier PCM can precede an error at physical EOF.

Every progress result returns the caller sample array.
Only a `Samples` event declares a valid sample prefix.
Other events leave that array unchanged.
The source and output limits apply to one encoded group and its PCM.
The metadata byte budget applies to metadata-only blocks and trailing tags.

```fern
<<*>>=
// Decode WavPack groups through a bounded sequential source window.
import "std/audio/wavpack";
import "std/audio/wavpack/block";
import "std/audio/wavpack/tags_input";
import "std/audio/pcm";
import "std/audio/source";
import "std/audio/wire";

pub struct Limits { codec: wavpack.Limits, input: source.Limits, max_metadata_bytes: i64 }

pub struct Audio { info: wavpack.Info, position: i64, frames: i32, offset: i64, bytes: i32 }

pub struct Summary { info: wavpack.Info, frames: i64, groups: i64, bytes_read: i64 }

pub enum Event { Begin(wavpack.Info), Samples(Audio), End(Summary) }

pub enum ReadEvent { Pending, Output(Event), Finished }

enum Stage { Probe, Header, Body, BeginTrack, Decode, Tags, EndTrack, Complete }

struct Control { limits: Limits, stage: Stage, cursor: i32, needed: i32, offset: i64, position: i64, groups: i64, metadata_bytes: i64, info: Option[wavpack.Info] }

pub opaque struct Input { control: Control, buffer: source.Buffer, builder: Option[wavpack.GroupBuilder], plan: Option[wavpack.GroupIndex], scratch: i32[], tags: Option[tags_input.TagInput] }

pub struct Progress { state: Input, event: ReadEvent, samples: i32[] }

pub function default_limits(): Limits {
  return Limits { codec: wavpack.default_limits(), input: source.Limits { chunk_size: 16384, max_buffer_bytes: 4194304 }, max_metadata_bytes: 67108864i64 };
}

function error(value: pcm.AudioError): source.ReadError {
  return source.ReadAudio(value);
}

function builder(info: Option[wavpack.Info], position: i64, limits: Limits): Result[wavpack.GroupBuilder, source.ReadError] {
  return match (wavpack.group_builder(info, position, limits.codec)) { Ok(value) => Ok(value), Err(e) => Err(error(e)) };
}

pub function create(limits: Limits): Result[Input, source.ReadError] {
  if (limits.codec.audio.max_input_bytes < 32 || limits.max_metadata_bytes < 0i64) {
    return Err(error(pcm.Limit));
  }
  let buffer = source.create(limits.input)?;
  let decoder = builder(None, 0i64, limits)?;
  let control = Control { limits: limits, stage: Probe, cursor: 0, needed: 4, offset: 0i64, position: 0i64, groups: 0i64, metadata_bytes: 0i64, info: None };
  return Ok(Input { control: control, buffer: buffer, builder: Some(decoder), plan: None, scratch: [], tags: None });
}

function progress(own state: Input, event: ReadEvent, own samples: i32[]): Result[Progress, source.ReadError] {
  return Ok(Progress { state: state, event: event, samples: samples });
}

function pending(own state: Input, own samples: i32[]): Result[Progress, source.ReadError] {
  return progress(state, Pending, samples);
}

function ceiling(control: Control): i32 {
  return if (control.limits.codec.audio.max_input_bytes < control.limits.input.max_buffer_bytes) { control.limits.codec.audio.max_input_bytes } else { control.limits.input.max_buffer_bytes };
}

function window(state: Input): [u8] {
  let count = if (state.buffer.available() < ceiling(state.control)) { state.buffer.available() } else { ceiling(state.control) };
  return state.buffer.view()[:count];
}

function offset(control: Control, count: i64): Result[Control, source.ReadError] {
  let Some(next) = control.offset +? count else {
    return Err(error(pcm.Limit));
  };
  return Ok(Control { ...control, offset: next });
}

function eof(control: Control): Result[Stage, source.ReadError] {
  if (control.cursor != 0) {
    return Err(error(pcm.Truncated));
  }
  if let None = control.info {
    return Err(error(pcm.Truncated));
  }
  return Ok(EndTrack);
}

function trailer(control: Control): Result[Stage, source.ReadError] {
  if (control.cursor != 0) {
    return Err(error(pcm.Header));
  }
  if let None = control.info {
    return Err(error(pcm.Header));
  }
  return Ok(Tags);
}

function probe(own state: Input, read: (i32) => Result[u8[], IoError], own samples: i32[]): Result[Progress, source.ReadError] {
  let Input { control, buffer, builder, plan, scratch, tags } = state;
  let (next, ready) = buffer.ensure(control.cursor + 4, read)?;
  let stage = Probe;
  if (ready) {
    stage = if (wire.tag(next.view(), control.cursor, "wvpk")) { Header } else { trailer(control)? };
  } else if (next.eof) {
    stage = if (next.available() == 0) { eof(control)? } else { trailer(control)? };
  }
  return pending(Input { control: Control { ...control, stage: stage }, buffer: next, builder: builder, plan: plan, scratch: scratch, tags: tags }, samples);
}

function declared_size(view: [u8], control: Control): Result[i32, source.ReadError] {
  let bytes = wire.le(view, control.cursor + 4, 4) + 8u64;
  if (bytes < 32u64 || bytes % 2u64 != 0u64) {
    return Err(error(pcm.Header));
  }
  if (bytes > (ceiling(control) - control.cursor) as u64) {
    return Err(error(pcm.Limit));
  }
  let frames = wire.le(view, control.cursor + 20, 4);
  if (frames > control.limits.codec.audio.max_samples as u64) {
    return Err(error(pcm.Limit));
  }
  if (frames == 0u64 && bytes as i64 > control.limits.max_metadata_bytes - control.metadata_bytes) {
    return Err(error(pcm.Limit));
  }
  return Ok(control.cursor + bytes as i32);
}

function header(own state: Input, read: (i32) => Result[u8[], IoError], own samples: i32[]): Result[Progress, source.ReadError] {
  let Input { control, buffer, builder, plan, scratch, tags } = state;
  let (next, ready) = buffer.ensure(control.cursor + 32, read)?;
  if (!ready && next.eof) {
    return Err(error(pcm.Truncated));
  }
  let needed = if (ready) { declared_size(next.view(), control)? } else { control.needed };
  return pending(Input { control: Control { ...control, stage: if (ready) { Body } else { Header }, needed: needed }, buffer: next, builder: builder, plan: plan, scratch: scratch, tags: tags }, samples);
}

function metadata(own state: Input, bytes: i32, own samples: i32[]): Result[Progress, source.ReadError] {
  let Input { control, buffer, builder: current, plan, scratch, tags } = state;
  let Some(value) = current else {
    return Err(error(pcm.Header));
  };
  let next = match (value.discard_metadata()) { Ok(value) => value, Err(e) => { return Err(error(e)); } };
  let counted = offset(control, bytes as i64)?;
  return pending(Input { control: Control { ...counted, stage: Probe, cursor: 0, metadata_bytes: counted.metadata_bytes + bytes as i64 }, buffer: buffer.advance(bytes)?, builder: Some(next), plan: plan, scratch: scratch, tags: tags }, samples);
}

function accepted(own state: Input, value: block.Block, own samples: i32[]): Result[Progress, source.ReadError] {
  let Input { control, buffer, builder: current, plan: _, scratch, tags } = state;
  let Some(decoder) = current else {
    return Err(error(pcm.Header));
  };
  let view = buffer.view();
  let count = if (buffer.available() < ceiling(control)) { buffer.available() } else { ceiling(control) };
  let result = match (decoder.push(view[:count], value)) { Ok(result) => result, Err(e) => { return Err(error(e)); } };
  match (result) {
    wavpack.GroupPending(next) => {
      let state = Input { control: Control { ...control, stage: Probe, cursor: value.end }, buffer: buffer, builder: Some(next), plan: None, scratch: scratch, tags: tags };
      if (value.frames == 0) {
        return metadata(state, value.end, samples);
      }
      return pending(state, samples);
    },
    wavpack.GroupReady(index) => {
      let stage = match (control.info) { None => BeginTrack, Some(_) => Decode };
      return pending(Input { control: Control { ...control, stage: stage }, buffer: buffer, builder: None, plan: Some(index), scratch: scratch, tags: tags }, samples);
    }
  }
}

function body(own state: Input, read: (i32) => Result[u8[], IoError], own samples: i32[]): Result[Progress, source.ReadError] {
  let Input { control, buffer, builder, plan, scratch, tags } = state;
  let (next, ready) = buffer.ensure(control.needed, read)?;
  if (!ready && next.eof) {
    return Err(error(pcm.Truncated));
  }
  let next_state = Input { control: control, buffer: next, builder: builder, plan: plan, scratch: scratch, tags: tags };
  if (!ready) {
    return pending(next_state, samples);
  }
  let value = match (block.read(window(next_state), control.cursor, control.limits.codec.max_metadata, control.limits.codec.audio)) { Ok(value) => value, Err(e) => { return Err(error(e)); } };
  return accepted(next_state, value, samples);
}

function begin(own state: Input, own samples: i32[]): Result[Progress, source.ReadError] {
  let Some(plan) = state.plan else {
    return Err(error(pcm.Header));
  };
  let info = plan.information();
  return progress(Input { ...state, control: Control { ...state.control, stage: Decode, info: Some(info) } }, Output(Begin(info)), samples);
}

function decode(own state: Input, own samples: i32[]): Result[Progress, source.ReadError] {
  let Input { control, buffer, builder: _, plan: current, scratch, tags } = state;
  let Some(plan) = current else {
    return Err(error(pcm.Header));
  };
  let bytes = plan.encoded_bytes();
  let (reused, frame) = match (plan.decode_into(buffer.view()[:bytes], scratch, samples)) { Ok(value) => value, Err(e) => { return Err(error(e)); } };
  let Some(position) = control.position +? frame.frames as i64 else {
    return Err(error(pcm.Limit));
  };
  let Some(groups) = control.groups +? 1i64 else {
    return Err(error(pcm.Limit));
  };
  let next = builder(Some(frame.info), position, control.limits)?;
  let counted = offset(control, bytes as i64)?;
  let audio = Audio { info: frame.info, position: frame.first_frame, frames: frame.frames, offset: control.offset, bytes: bytes };
  return progress(Input { control: Control { ...counted, stage: Probe, cursor: 0, position: position, groups: groups, info: Some(frame.info) }, buffer: buffer.advance(bytes)?, builder: Some(next), plan: None, scratch: reused, tags: tags }, Output(Samples(audio)), frame.samples);
}

function start_tags(own state: Input, own samples: i32[]): Result[Progress, source.ReadError] {
  let tag = tags_input.create(state.control.limits.max_metadata_bytes - state.control.metadata_bytes, state.control.limits.codec.max_metadata)?;
  return pending(Input { ...state, tags: Some(tag) }, samples);
}

function tag_step(own state: Input, read: (i32) => Result[u8[], IoError], own samples: i32[]): Result[Progress, source.ReadError] {
  let Input { control, buffer, builder, plan, scratch, tags: current } = state;
  let Some(tag) = current else {
    return start_tags(Input { control: control, buffer: buffer, builder: builder, plan: plan, scratch: scratch, tags: None }, samples);
  };
  let before = tag.bytes_read();
  let next = tag.step(buffer, read)?;
  let consumed = next.state.bytes_read() - before;
  let counted = offset(control, consumed)?;
  return pending(Input { control: Control { ...counted, stage: if (next.done) { EndTrack } else { Tags }, metadata_bytes: counted.metadata_bytes + consumed }, buffer: next.buffer, builder: builder, plan: plan, scratch: scratch, tags: Some(next.state) }, samples);
}

function finish(own state: Input, own samples: i32[]): Result[Progress, source.ReadError] {
  let Some(info) = state.control.info else {
    return Err(error(pcm.Truncated));
  };
  if (info.total_frames >= 0i64 && info.total_frames != state.control.position) {
    return Err(error(pcm.Truncated));
  }
  let actual = wavpack.Info { ...info, total_frames: state.control.position };
  let summary = Summary { info: actual, frames: state.control.position, groups: state.control.groups, bytes_read: state.control.offset };
  return progress(Input { ...state, control: Control { ...state.control, stage: Complete, info: Some(actual) } }, Output(End(summary)), samples);
}

pub function (own state: Input) step_into_with(read: (i32) => Result[u8[], IoError], own samples: i32[]): Result[Progress, source.ReadError] {
  return match (state.control.stage) {
    Probe => probe(state, read, samples),
    Header => header(state, read, samples),
    Body => body(state, read, samples),
    BeginTrack => begin(state, samples),
    Decode => decode(state, samples),
    Tags => tag_step(state, read, samples),
    EndTrack => finish(state, samples),
    Complete => progress(state, Finished, samples),
  };
}

pub function (own state: Input) step_into(reader: Reader, own samples: i32[]): Result[Progress, source.ReadError] {
  return state.step_into_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), samples);
}

pub function (own state: Input) next_into_with(read: (i32) => Result[u8[], IoError], own samples: i32[]): Result[Progress, source.ReadError] {
  while (true) {
    let Progress { state: next, event, samples: output } = state.step_into_with(read, samples)?;
    if let Pending = event {
      state = next;
      samples = output;
    } else {
      return progress(next, event, output);
    }
  }
}

pub function (own state: Input) next_into(reader: Reader, own samples: i32[]): Result[Progress, source.ReadError] {
  return state.next_into_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), samples);
}

pub function (state: Input) information(): Option[wavpack.Info] {
  return state.control.info;
}

pub function (state: Input) buffered_bytes(): i32 {
  return state.buffer.available();
}
```
