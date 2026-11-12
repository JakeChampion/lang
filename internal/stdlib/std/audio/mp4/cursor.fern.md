# Bounded AAC MP4 presentation reads

This module reads native AAC PCM from an existing MP4 index and a borrowed seekable source.
It reuses the AAC decoder, packet windows, and ISO presentation plan.
The cursor retains AAC state and scratch, one encoded packet, and one decoded AAC frame.
It retains no complete encoded input or presentation PCM.
The caller supplies reusable float PCM storage. Only the declared output prefix contains valid samples.

Each step performs at most one seek, one bounded source read, or one AAC packet decode.
Pending and finished events return the caller storage unchanged.
The existing sample budget bounds the complete presentation length, independently of retained frame storage.
The operation budget applies to each cursor read sequence and resets after seek.

Positions count frames across all channels. Normal edits remove priming and padding. Empty edits produce bounded silence blocks.
Repeated or backward edits reset the decoder and replay earlier packets to restore overlap state.
An exact seek uses the same replay from the start. A seek to the declared end requires no source operations.

The caller owns the immutable source and closes it after use.
The caller must preserve its position during a pending packet read.
Errors consume cursor state. Source I/O failures remain distinct from codec errors.
These calls are synchronous and belong outside latency-sensitive audio callbacks.
This cursor does not control Android audio transport or synchronize video.

```fern
<<*>>=
// Read bounded AAC presentation blocks through the existing packet window.
import "std/audio/aac";
import "std/audio/aac/dsp";
import "std/audio/mp4";
import "std/audio/mp4/file";
import "std/audio/mp4/presentation";
import "std/audio/mp4/time";
import "std/audio/mp4/tables";
import "std/audio/mp4/source" as metadata;
import "std/audio/mp4/index" as container;
import "std/audio/file" as packet;
import "std/audio/source";
import "std/audio/pcm";
import "std/i32";
import "std/i64";

pub struct Info { sample_rate: i32, channels: i32, channel_config: i32, frames: i64, track_id: u32 }

pub struct Audio { info: Info, position: i64, frames: i32, samples: f32[] }

pub enum BufferEvent { BufferPending(f32[]), BufferSamples(Audio), BufferFinished(f32[]) }

pub opaque struct Cursor { track: mp4.Track, plan: presentation.Plan, limits: file.Limits, codec: aac.Decoder, packet: i32, samples: f32[], position: i32, operations: i32, window: Option[packet.Window] }

function error(value: pcm.AudioError): source.ReadError {
  return source.ReadAudio(value);
}

function check_index(index: mp4.Index, number: i32, limits: file.Limits): Result[mp4.Track, source.ReadError] {
  let scan = metadata.Limits { input: limits.input, max_file_bytes: limits.max_file_bytes, max_metadata_bytes: limits.max_metadata_bytes, max_input_bytes: limits.index.audio.max_input_bytes, max_boxes: limits.index.max_boxes, max_operations: limits.max_operations };
  metadata.validate(index.bytes, scan)?;
  let _ = source.create(limits.input)?;
  if let Err(value) = container.validate_limits([], limits.index) {
    return Err(error(value));
  }
  if (number < 0 || number >= index.tracks.len()) {
    return Err(error(pcm.Header));
  }
  let track = index.tracks[number];
  if (index.tracks.len() > limits.index.max_tracks || track.packets.len() > limits.index.max_packets || track.configurations.len() > limits.index.max_descriptions || track.edits.len() > limits.index.max_edits) {
    return Err(error(pcm.Limit));
  }
  return Ok(track);
}

function check_range(row: tables.Packet, bytes: i64, descriptions: i32): Result[void, source.ReadError] {
  if (row.offset < 0i64 || row.bytes < 1 || row.offset > bytes || row.bytes as i64 > bytes - row.offset) {
    return Err(error(pcm.Header));
  }
  if (row.position < 0i64 || row.duration < 1 || row.description < 0 || row.description >= descriptions) {
    return Err(error(pcm.Header));
  }
  return Ok(());
}

function check_packets(track: mp4.Track, bytes: i64, rate: i32, limits: file.Limits): Result[void, source.ReadError] {
  for i in 0..track.packets.len() {
    let row = track.packets[i];
    check_range(row, bytes, track.configurations.len())?;
    let Some(_) = row.position +? row.duration as i64 else {
      return Err(error(pcm.Limit));
    };
    if (row.bytes > limits.index.audio.max_input_bytes) {
      return Err(error(pcm.Limit));
    }
    if (row.bytes > limits.input.max_buffer_bytes) {
      return Err(source.ReadSize);
    }
    let at = match (time.rescale(row.position, rate as u32, track.clock.scale)) { Ok(value) => value, Err(value) => { return Err(error(value)); } };
    let first = i as i64 * 1024i64;
    if (at < first - 1i64 || at > first + 1i64) {
      return Err(error(pcm.Unsupported));
    }
  }
  return Ok(());
}

function check_clocks(movie: time.Clock, track: time.Clock): Result[void, source.ReadError] {
  if (movie.scale == 0u32 || track.scale == 0u32) {
    return Err(error(pcm.Header));
  }
  return Ok(());
}

pub function create(index: mp4.Index, number: i32, limits: file.Limits): Result[Cursor, source.ReadError] {
  let track = check_index(index, number, limits)?;
  check_clocks(index.movie, track.clock)?;
  let configuration = match (mp4.packet_config(track)) { Ok(value) => value, Err(value) => { return Err(error(value)); } };
  check_packets(track, index.bytes, configuration.sample_rate, limits)?;
  let plan = match (presentation.create(track.packets, track.edits, track.clock, index.movie, configuration.sample_rate, configuration.channels, track.packets.len() as i64 * 1024i64, limits.index.audio)) { Ok(value) => value, Err(value) => { return Err(error(value)); } };
  let codec = match (aac.create(configuration, limits.index.audio)) { Ok(value) => value, Err(value) => { return Err(error(value)); } };
  return Ok(Cursor { track: track, plan: plan, limits: limits, codec: codec, packet: 0, samples: dsp.zeros(1024 * configuration.channels), position: 0, operations: 0, window: None });
}

pub function (state: Cursor) info(): Info {
  let configuration = state.codec.configuration();
  return Info { sample_rate: configuration.sample_rate, channels: configuration.channels, channel_config: configuration.channel_config, frames: state.plan.frames as i64, track_id: state.track.id };
}

function reset(own state: Cursor, position: i32): Cursor {
  let Cursor { track, plan, limits, codec, packet, samples, position: old, operations, window } = state;
  return Cursor { track: track, plan: plan, limits: limits, codec: codec.reset(), packet: 0, samples: samples, position: position, operations: 0, window: None };
}

pub function (own state: Cursor) seek(position: i64): Result[Cursor, source.ReadError] {
  if (position < 0i64 || position > state.plan.frames as i64) {
    return Err(error(pcm.Header));
  }
  return Ok(reset(state, position as i32));
}

function region(plan: presentation.Plan, position: i32, channels: i32): (i64, i32) {
  for span in plan.spans {
    let first = span.output / channels;
    let end = first as i64 + span.end - span.start;
    if (position < first) {
      return (-1i64, (first - position).min(1024));
    }
    if (position as i64 < end) {
      let at = span.start + (position - first) as i64;
      let count = (span.end - at).min(1024i64) as i32;
      return (at, count.min(1024 - (at % 1024i64) as i32));
    }
  }
  return (-1i64, (plan.frames - position).min(1024));
}

function output(own state: Cursor, own samples: f32[], at: i64, frames: i32): (Cursor, BufferEvent) {
  let info = state.info();
  let count = frames * info.channels;
  for i in samples.len()..count {
    samples = samples.append(0.0f32);
  }
  let start = (at % 1024i64) as i32 * info.channels;
  for i in 0..count {
    samples = samples.with(i, if (at < 0i64) { 0.0f32 } else { state.samples[start + i] });
  }
  let audio = Audio { info: info, position: state.position as i64, frames: frames, samples: samples };
  return (Cursor { ...state, position: state.position + frames }, BufferSamples(audio));
}

function decode(own state: Cursor, input: [u8]): Result[Cursor, source.ReadError] {
  let Cursor { track, plan, limits, codec, packet, samples, position, operations, window } = state;
  let (next, frame) = match (codec.decode_packet_into(input, samples, limits.index.audio)) { Ok(value) => value, Err(value) => { return Err(error(value)); } };
  return Ok(Cursor { track: track, plan: plan, limits: limits, codec: next, packet: packet + 1, samples: frame.samples, position: position, operations: operations, window: None });
}

function take_window(own state: Cursor): (Cursor, Option[packet.Window]) {
  let Cursor { track, plan, limits, codec, packet, samples, position, operations, window } = state;
  return (Cursor { track: track, plan: plan, limits: limits, codec: codec, packet: packet, samples: samples, position: position, operations: operations, window: None }, window);
}

function read_packet(own state: Cursor, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[Cursor, source.ReadError] {
  let (rest, pending) = take_window(state);
  let Some(window) = pending else {
    let row = rest.track.packets[rest.packet];
    let next = packet.create(row.offset, row.bytes, rest.limits.input)?;
    return Ok(Cursor { ...rest, window: Some(next) });
  };
  if (!window.ready) {
    let next = window.step_with(seek, read)?;
    return Ok(Cursor { ...rest, window: Some(next) });
  }
  return decode(rest, window.view());
}

pub function (own state: Cursor) step_into_with(own samples: f32[], seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Cursor, BufferEvent), source.ReadError] {
  if (state.position == state.plan.frames) {
    return Ok((state, BufferFinished(samples)));
  }
  if (state.operations >= state.limits.max_operations) {
    return Err(error(pcm.Limit));
  }
  state = Cursor { ...state, operations: state.operations + 1 };
  let (at, frames) = region(state.plan, state.position, state.info().channels);
  if (at < 0i64 || at >= (state.packet - 1) as i64 * 1024i64 && at < state.packet as i64 * 1024i64) {
    return Ok(output(state, samples, at, frames));
  }
  if (at < (state.packet - 1) as i64 * 1024i64) {
    let used = state.operations;
    let position = state.position;
    return Ok((Cursor { ...reset(state, position), operations: used }, BufferPending(samples)));
  }
  return Ok((read_packet(state, seek, read)?, BufferPending(samples)));
}

pub function (own state: Cursor) step_into(own samples: f32[], reader: Reader): Result[(Cursor, BufferEvent), source.ReadError] {
  return state.step_into_with(samples, (at: i64): Result[i64, IoError] => reader.seek(at, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
