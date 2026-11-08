# Bounded MP3 input

This module decodes sequential MP3 input without a complete encoded track in memory.
It accepts a borrowed Fern `Reader` or a bounded byte-source callback.
Output uses interleaved float PCM at the track sample rate.

The reader applies recognized Xing/LAME delay and padding once.
It retains a bounded encoded window, codec history, and the final padding samples.
Large ID3 payloads pass through the window without retention.
Trailing ID3v1 and APE tags require at most 192 retained metadata bytes.

Each `step` performs at most one source read or one frame decode.
Pending steps let the application pause or cancel between operations.
`next` continues until a track event or the end.
An error consumes the input state. The caller retains and closes the source.

The reader validates checksums and a constant output format as frames arrive.
It checks declared frame counts, padding, and trailing metadata at physical EOF.
Earlier PCM events can precede a later validation error.
`End` confirms complete validation. Subsequent reads return `Finished` without source reads.

A non-seekable source needs a Xing frame count when a headerless APE payload contains complete MP3 frames.

```fern
<<*>>=
// Decode sequential MP3 with bounded encoded input and gapless output.

import "std/audio/pcm";
import "std/audio/source";
import "std/audio/wire";
import "std/audio/mp3";
import "std/audio/mp3/header";
import "std/audio/mp3/metadata";

pub struct Limits { audio: pcm.Limits, input: source.Limits, max_metadata_bytes: i64 }

pub struct Info { sample_rate: i32, channels: i32, skip_frames: i32, padding_frames: i32, declared_frames: u32, data_offset: i64 }

pub struct Audio { info: Info, position: i64, frames: i32, samples: f32[] }

pub struct Summary { info: Info, frames: i64, encoded_frames: i64, bytes_read: i64 }

pub enum Event { Begin(Info), Samples(Audio), End(Summary) }

pub enum ReadEvent { Pending, Output(Event), Finished }

enum Stage { Scan, SkipTag, TagEnd, ReadHeader, ReadFrame, Decode, ReadTrailer, Complete }

pub struct Input {
  limits: Limits,
  buffer: source.Buffer,
  codec: mp3.Decoder,
  stage: Stage,
  remaining: i32,
  id3: u8[],
  info: Option[Info],
  tail: f32[],
  raw_frames: i64,
  encoded_frames: i64,
  position: i64,
  offset: i64,
  metadata_bytes: i64,
  trailer: metadata.Trailer,
  frame_bytes: i32,
  failure: pcm.AudioError,
}

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), input: source.default_limits(), max_metadata_bytes: 67108864i64 };
}

function audio_error(e: pcm.AudioError): source.ReadError {
  return source.ReadAudio(e);
}

pub function create(limits: Limits): Result[Input, source.ReadError] {
  if (limits.audio.max_input_bytes < 0 || limits.audio.max_samples < 0 || limits.max_metadata_bytes < 0i64) {
    return Err(audio_error(pcm.Limit));
  }
  let buffer = source.create(limits.input)?;
  let trailer = match (metadata.trailer(limits.max_metadata_bytes)) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  return Ok(Input { limits: limits, buffer: buffer, codec: mp3.decoder(), stage: Scan, remaining: 0, id3: [], info: None, tail: [], raw_frames: 0i64, encoded_frames: 0i64, position: 0i64, offset: 0i64, metadata_bytes: 0i64, trailer: trailer, frame_bytes: 0, failure: pcm.Header });
}

function consume(own state: Input, count: i32): Result[Input, source.ReadError] {
  let Some(offset) = state.offset +? count as i64 else {
    return Err(audio_error(pcm.Limit));
  };
  return Ok(Input { ...state, buffer: state.buffer.advance(count)?, offset: offset });
}

function ensure(own state: Input, count: i32, read: (i32) => Result[u8[], IoError]): Result[(Input, boolean), source.ReadError] {
  let (buffer, ready) = state.buffer.ensure(count, read)?;
  if (!ready && buffer.eof) {
    return Err(audio_error(pcm.Truncated));
  }
  return Ok((Input { ...state, buffer: buffer }, ready));
}

function pending(own state: Input): Result[(Input, ReadEvent), source.ReadError] {
  return Ok((state, Pending));
}

function scan(own state: Input, read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  let (ready, complete) = ensure(state, 3, read)?;
  if (!complete) {
    return pending(ready);
  }
  if (!wire.tag(ready.buffer.view(), 0, "ID3")) {
    return pending(Input { ...ready, stage: ReadHeader });
  }
  return pending(Input { ...ready, stage: TagEnd, remaining: -1 });
}

function tag_header(own state: Input, read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  let (ready, complete) = ensure(state, 10, read)?;
  if (!complete) {
    return pending(ready);
  }
  let value = match (metadata.inspect_id3(ready.buffer.view())) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  let total = value.payload_bytes as i64 + 10i64 + (if (value.footer) { 10i64 } else { 0i64 });
  if (total > ready.limits.max_metadata_bytes - ready.metadata_bytes) {
    return Err(audio_error(pcm.Limit));
  }
  let bytes: u8[] = [];
  for byte in ready.buffer.view()[:10] {
    bytes = bytes.append(byte);
  }
  let next = consume(ready, 10)?;
  return pending(Input { ...next, metadata_bytes: next.metadata_bytes + total, id3: bytes, remaining: value.payload_bytes, stage: SkipTag });
}

function skip_tag(own state: Input, read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  if (state.remaining == 0) {
    let footer = (state.id3[5] as i32 & 16) != 0;
    if (footer) {
      return pending(Input { ...state, stage: TagEnd });
    }
    return pending(Input { ...state, id3: [], stage: Scan });
  }
  let (ready, complete) = ensure(state, 1, read)?;
  if (!complete) {
    return pending(ready);
  }
  let count = if (ready.remaining < ready.buffer.available()) { ready.remaining } else { ready.buffer.available() };
  let remaining = ready.remaining - count;
  let next = consume(ready, count)?;
  return pending(Input { ...next, remaining: remaining });
}

function tag_end(own state: Input, read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  if (state.remaining == -1) {
    return tag_header(state, read);
  }
  let (ready, complete) = ensure(state, 10, read)?;
  if (!complete) {
    return pending(ready);
  }
  if let Err(e) = metadata.check_footer(ready.buffer.view(), ready.id3) {
    return Err(audio_error(e));
  }
  let next = consume(ready, 10)?;
  return pending(Input { ...next, id3: [], stage: Scan });
}

function finish(own state: Input): Result[(Input, ReadEvent), source.ReadError] {
  let Some(info) = state.info else {
    return Err(audio_error(pcm.Truncated));
  };
  if (state.encoded_frames == 0i64) {
    return Err(audio_error(pcm.Truncated));
  }
  if (info.declared_frames != 0u32 && state.encoded_frames != info.declared_frames as i64) {
    return Err(audio_error(pcm.Header));
  }
  if (state.raw_frames < info.skip_frames as i64 + info.padding_frames as i64) {
    return Err(audio_error(pcm.Header));
  }
  let summary = Summary { info: info, frames: state.position, encoded_frames: state.encoded_frames, bytes_read: state.offset };
  return Ok((Input { ...state, tail: [], stage: Complete }, Output(End(summary))));
}

function begin_trailer(own state: Input, failure: pcm.AudioError): Result[(Input, ReadEvent), source.ReadError] {
  let Some(_) = state.info else {
    return Err(audio_error(failure));
  };
  let trailer = match (metadata.trailer(state.limits.max_metadata_bytes - state.metadata_bytes)) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  return pending(Input { ...state, stage: ReadTrailer, trailer: trailer, failure: failure });
}

function read_header(own state: Input, read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  let (buffer, complete) = state.buffer.ensure(4, read)?;
  let ready = Input { ...state, buffer: buffer };
  if (!complete) {
    if (!ready.buffer.eof) {
      return pending(ready);
    }
    if (ready.buffer.available() == 0) {
      return finish(ready);
    }
    return begin_trailer(ready, pcm.Truncated);
  }
  return frame_header(ready);
}

function frame_header(own ready: Input): Result[(Input, ReadEvent), source.ReadError] {
  if let Some(info) = ready.info {
    if (info.declared_frames != 0u32 && ready.encoded_frames == info.declared_frames as i64) {
      return begin_trailer(ready, pcm.Header);
    }
  }
  let h = match (header.parse(ready.buffer.view(), 0)) { Ok(value) => value, Err(e) => { return begin_trailer(ready, e); } };
  if (h.bytes > ready.limits.audio.max_input_bytes) {
    return Err(audio_error(pcm.Limit));
  }
  return pending(Input { ...ready, stage: ReadFrame, frame_bytes: h.bytes });
}

function initial(own state: Input): Result[(Input, ReadEvent), source.ReadError] {
  let track = match (mp3.inspect_first_frame(state.buffer.view()[:state.frame_bytes], 0, state.limits.audio)) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  let Some(offset) = state.offset +? track.data_offset as i64 else {
    return Err(audio_error(pcm.Limit));
  };
  let info = Info { sample_rate: track.sample_rate, channels: track.channels, skip_frames: track.skip_frames, padding_frames: track.padding_frames, declared_frames: track.declared_frames, data_offset: offset };
  let next = Input { ...state, info: Some(info), stage: Decode };
  if (track.data_offset != 0) {
    let after = consume(next, track.data_offset)?;
    return Ok((Input { ...after, stage: ReadHeader }, Output(Begin(info))));
  }
  return Ok((next, Output(Begin(info))));
}

function read_frame(own state: Input, read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  let count = state.frame_bytes;
  let (ready, complete) = ensure(state, count, read)?;
  if (!complete) {
    return pending(ready);
  }
  if let None = ready.info {
    return initial(ready);
  }
  return pending(Input { ...ready, stage: Decode });
}

function floats(input: [f32], start: i32, end: i32): f32[] {
  let out: f32[] = [];
  for i in start..end {
    out = out.append(input[i]);
  }
  return out;
}

function playable(own state: Input, own frame: mp3.Frame, info: Info): Result[(Input, ReadEvent), source.ReadError] {
  let start = if (state.raw_frames < info.skip_frames as i64) { info.skip_frames as i64 - state.raw_frames } else { 0i64 };
  let skip = if (start > frame.frames as i64) { frame.frames } else { start as i32 };
  let combined = state.tail;
  for i in skip * info.channels..frame.samples.len() {
    combined = combined.append(frame.samples[i]);
  }
  let kept = info.padding_frames * info.channels;
  let count = if (combined.len() > kept) { combined.len() - kept } else { 0 };
  let tail = floats(combined, count, combined.len());
  let Some(raw) = state.raw_frames +? frame.frames as i64 else {
    return Err(audio_error(pcm.Limit));
  };
  let Some(encoded) = state.encoded_frames +? 1i64 else {
    return Err(audio_error(pcm.Limit));
  };
  let next = Input { ...state, raw_frames: raw, encoded_frames: encoded, tail: tail, stage: ReadHeader };
  if (count == 0) {
    return pending(next);
  }
  let frames = count / info.channels;
  let Some(position) = next.position +? frames as i64 else {
    return Err(audio_error(pcm.Limit));
  };
  let audio = Audio { info: info, position: next.position, frames: frames, samples: floats(combined, 0, count) };
  return Ok((Input { ...next, position: position }, Output(Samples(audio))));
}

function decode(own state: Input): Result[(Input, ReadEvent), source.ReadError] {
  let Some(info) = state.info else {
    return Err(audio_error(pcm.Header));
  };
  let h = match (header.parse(state.buffer.view(), 0)) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  if (h.sample_rate != info.sample_rate || h.channels != info.channels) {
    return Err(audio_error(pcm.Unsupported));
  }
  let (codec, result) = state.codec.decode_frame(state.buffer.view()[:state.frame_bytes], 0, state.limits.audio);
  let frame = match (result) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  if (frame.needs_history) {
    return Err(audio_error(pcm.Header));
  }
  let next = consume(Input { ...state, codec: codec }, frame.bytes_read)?;
  return playable(next, frame, info);
}

function read_trailer(own state: Input, read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  let (buffer, _) = state.buffer.ensure(1, read)?;
  let ready = Input { ...state, buffer: buffer };
  let count = ready.buffer.available();
  if (count == 0 && ready.buffer.eof) {
    if let Err(e) = ready.trailer.finish() {
      return Err(audio_error(if (e == pcm.Header) { ready.failure } else { e }));
    }
    return finish(ready);
  }
  let trailer = match (ready.trailer.feed(ready.buffer.view())) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  let next = consume(Input { ...ready, trailer: trailer }, count)?;
  return pending(next);
}

pub function (own state: Input) step_with(read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  match (state.stage) {
    Scan => {
      return scan(state, read);
    },
    SkipTag => {
      return skip_tag(state, read);
    },
    TagEnd => {
      return tag_end(state, read);
    },
    ReadHeader => {
      return read_header(state, read);
    },
    ReadFrame => {
      return read_frame(state, read);
    },
    Decode => {
      return decode(state);
    },
    ReadTrailer => {
      return read_trailer(state, read);
    },
    Complete => {
      return Ok((state, Finished));
    }
  }
}

pub function (own state: Input) step(reader: Reader): Result[(Input, ReadEvent), source.ReadError] {
  return state.step_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}

pub function (own state: Input) next_with(read: (i32) => Result[u8[], IoError]): Result[(Input, Option[Event]), source.ReadError] {
  while (true) {
    let (next, event) = state.step_with(read)?;
    state = next;
    match (event) {
      Output(value) => {
        return Ok((state, Some(value)));
      },
      Finished => {
        return Ok((state, None));
      },
      Pending => {}
    }
  }
}

pub function (own state: Input) next(reader: Reader): Result[(Input, Option[Event]), source.ReadError] {
  return state.next_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
