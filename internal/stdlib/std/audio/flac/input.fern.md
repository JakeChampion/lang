# Bounded native FLAC input

This module decodes a sequential native FLAC stream from a borrowed `Reader` or a byte-source callback.
It retains a bounded encoded window, one frame of integer PCM, and an incremental MD5 state.
Metadata payloads pass through the window without retention.
The sample limit applies to one frame. Track positions use 64-bit counts.

The default encoded window is 4 MiB, with source reads of at most 16 KiB.
`audio.max_input_bytes` also bounds an individual encoded frame.
The metadata budget includes the file signature and all metadata headers.

Each `step` performs at most one source read or one frame decode attempt.
`next` continues until an identification, sample, or completion event.
Short source reads increase the next decode threshold geometrically.
A malformed frame can require more input before the reader reports its error.
The encoded window limits that additional input.

`End` confirms frame checksums, frame sequence, declared sample counts, and a supplied STREAMINFO MD5.
Earlier PCM events can precede an error at physical EOF.
An empty successful source read means permanent EOF. I/O errors retain their own category.
The caller owns and closes the reader after success or failure.

```fern
<<*>>=
// Decode sequential native FLAC with bounded encoded input and complete validation.

import "std/audio/flac";
import "std/audio/pcm";
import "std/audio/source";
import "std/audio/wire";
import "std/crypto";

pub struct Limits { audio: pcm.Limits, input: source.Limits, max_metadata_bytes: i64 }

pub struct Info { format: pcm.Format, total_frames: u64, min_block: i32, max_block: i32, data_offset: i64, md5: u8[] }

pub struct Audio { info: Info, position: i64, frames: i32, samples: i32[] }

pub struct Summary { info: Info, frames: i64, encoded_frames: i64, bytes_read: i64 }

pub enum Event { Begin(Info), Samples(Audio), End(Summary) }

pub enum ReadEvent { Pending, Output(Event), Finished }

enum Stage { StreamInfo, MetadataHeader, SkipMetadata, BeginTrack, ReadFrame, Decode, Complete }

pub struct Input {
  limits: Limits,
  buffer: source.Buffer,
  stage: Stage,
  header: Option[flac.Header],
  info: Option[Info],
  last_metadata: boolean,
  remaining: i32,
  offset: i64,
  metadata_bytes: i64,
  max_frame_bytes: i32,
  min_frame_bytes: i32,
  needed: i32,
  position: i64,
  encoded_frames: i64,
  variable: boolean,
  block_size: i32,
  previous: i32,
  hash: crypto.Md5,
  check_md5: boolean,
}

pub function default_limits(): Limits {
  return Limits { audio: pcm.default_limits(), input: source.Limits { chunk_size: 16384, max_buffer_bytes: 4194304 }, max_metadata_bytes: 67108864i64 };
}

function audio_error(e: pcm.AudioError): source.ReadError {
  return source.ReadAudio(e);
}

pub function create(limits: Limits): Result[Input, source.ReadError] {
  if (limits.audio.max_input_bytes < 1 || limits.audio.max_samples < 0 || limits.max_metadata_bytes < 42i64) {
    return Err(audio_error(pcm.Limit));
  }
  let buffer = source.create(limits.input)?;
  return Ok(Input { limits: limits, buffer: buffer, stage: StreamInfo, header: None, info: None, last_metadata: false, remaining: 0, offset: 0i64, metadata_bytes: 0i64, max_frame_bytes: 0, min_frame_bytes: 0, needed: 6, position: 0i64, encoded_frames: 0i64, variable: false, block_size: 0, previous: 0, hash: crypto.md5_new(), check_md5: false });
}

function consume(own state: Input, count: i32): Result[Input, source.ReadError] {
  let Some(offset) = state.offset +? count as i64 else {
    return Err(audio_error(pcm.Limit));
  };
  return Ok(Input { ...state, buffer: state.buffer.advance(count)?, offset: offset });
}

function pending(own state: Input): Result[(Input, ReadEvent), source.ReadError] {
  return Ok((state, Pending));
}

function ensure(own state: Input, count: i32, read: (i32) => Result[u8[], IoError]): Result[(Input, boolean), source.ReadError] {
  let (buffer, ready) = state.buffer.ensure(count, read)?;
  if (!ready && buffer.eof) {
    return Err(audio_error(pcm.Truncated));
  }
  return Ok((Input { ...state, buffer: buffer }, ready));
}

function supplied(digest: [u8]): boolean {
  for byte in digest {
    if (byte != 0u8) {
      return true;
    }
  }
  return false;
}

function stream_info(own state: Input, read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  let (ready, complete) = ensure(state, 42, read)?;
  if (!complete) {
    return pending(ready);
  }
  let bytes: u8[] = [];
  for byte in ready.buffer.view()[:42] {
    bytes = bytes.append(byte);
  }
  let last = (bytes[4] & 128u8) != 0u8;
  bytes = bytes.with(4, bytes[4] | 128u8);
  let header = match (flac.inspect(bytes, pcm.Limits { ...ready.limits.audio, max_input_bytes: 42 })) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  let _ = match (pcm.sample_count(header.max_block, header.format.channels, ready.limits.audio)) { Ok(value) => value, Err(e) => { return Err(audio_error(e)); } };
  let next = consume(ready, 42)?;
  return pending(Input { ...next, header: Some(header), last_metadata: last, metadata_bytes: 42i64, min_frame_bytes: wire.be(bytes, 12, 3) as i32, max_frame_bytes: wire.be(bytes, 15, 3) as i32, check_md5: supplied(header.md5), stage: if (last) { BeginTrack } else { MetadataHeader } });
}

function metadata_header(own state: Input, read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  let (ready, complete) = ensure(state, 4, read)?;
  if (!complete) {
    return pending(ready);
  }
  let bytes = ready.buffer.view();
  let kind = bytes[0] as i32 & 127;
  if (kind == 0 || kind == 127) {
    return Err(audio_error(pcm.Header));
  }
  let remaining = wire.be(bytes, 1, 3) as i32;
  let total = remaining as i64 + 4i64;
  if (total > ready.limits.max_metadata_bytes - ready.metadata_bytes) {
    return Err(audio_error(pcm.Limit));
  }
  let last = (bytes[0] as i32 & 128) != 0;
  let next = consume(ready, 4)?;
  let seen = next.metadata_bytes + total;
  return pending(Input { ...next, remaining: remaining, last_metadata: last, metadata_bytes: seen, stage: SkipMetadata });
}

function skip_metadata(own state: Input, read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  if (state.remaining == 0) {
    return pending(Input { ...state, stage: if (state.last_metadata) { BeginTrack } else { MetadataHeader } });
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

function begin(own state: Input): Result[(Input, ReadEvent), source.ReadError] {
  let Some(header) = state.header else {
    return Err(audio_error(pcm.Header));
  };
  let info = Info { format: header.format, total_frames: header.frames, min_block: header.min_block, max_block: header.max_block, data_offset: state.offset, md5: header.md5 };
  return Ok((Input { ...state, info: Some(info), stage: ReadFrame }, Output(Begin(info))));
}

function finish(own state: Input): Result[(Input, ReadEvent), source.ReadError] {
  let Some(info) = state.info else {
    return Err(audio_error(pcm.Truncated));
  };
  if (state.encoded_frames == 0i64 || info.total_frames != 0u64 && state.position as u64 != info.total_frames) {
    return Err(audio_error(pcm.Truncated));
  }
  if (state.check_md5) {
    let actual = state.hash.finish();
    if (actual != info.md5) {
      return Err(audio_error(pcm.Checksum));
    }
  }
  let summary = Summary { info: info, frames: state.position, encoded_frames: state.encoded_frames, bytes_read: state.offset };
  return Ok((Input { ...state, stage: Complete }, Output(End(summary))));
}

function ceiling(state: Input): i32 {
  return if (state.limits.audio.max_input_bytes < state.limits.input.max_buffer_bytes) { state.limits.audio.max_input_bytes } else { state.limits.input.max_buffer_bytes };
}

function read_frame(own state: Input, read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  let needed = if (state.needed < ceiling(state)) { state.needed } else { ceiling(state) };
  let (buffer, complete) = state.buffer.ensure(needed, read)?;
  let next = Input { ...state, buffer: buffer };
  if (!complete && !next.buffer.eof) {
    return pending(next);
  }
  if (next.buffer.eof && next.buffer.available() == 0) {
    return finish(next);
  }
  return pending(Input { ...next, stage: Decode });
}

function retry(own state: Input, e: pcm.AudioError): Result[(Input, ReadEvent), source.ReadError] {
  if (e == pcm.Limit || state.buffer.eof) {
    return Err(audio_error(e));
  }
  let cap = ceiling(state);
  let available = state.buffer.available();
  if (available >= cap) {
    return Err(audio_error(pcm.Limit));
  }
  let needed = if (available > cap / 2) { cap } else { available * 2 };
  return pending(Input { ...state, needed: needed, stage: ReadFrame });
}

function follows(state: Input, frame: flac.Frame): boolean {
  if (state.encoded_frames == 0i64) {
    return frame.number == 0u64;
  }
  if (frame.variable != state.variable) {
    return false;
  }
  let number = if (state.variable) { state.position as u64 } else { state.encoded_frames as u64 };
  return frame.number == number && (state.variable || state.previous == state.block_size && frame.frames <= state.block_size);
}

function hash_frame(hash: crypto.Md5, frame: flac.Frame): crypto.Md5 {
  let bytes: u8[] = [];
  let width = (frame.format.bits_per_sample + 7) / 8;
  for sample in frame.samples {
    bytes = wire.put_le(bytes, sample as u64, width);
  }
  return hash.update(bytes);
}

function counted(state: Input, frame: flac.Frame, info: Info): Result[(i64, i64), source.ReadError] {
  if (!follows(state, frame)) {
    return Err(audio_error(pcm.Header));
  }
  if (state.max_frame_bytes != 0 && frame.bytes_read > state.max_frame_bytes || state.min_frame_bytes != 0 && frame.bytes_read < state.min_frame_bytes) {
    return Err(audio_error(pcm.Header));
  }
  let Some(position) = state.position +? frame.frames as i64 else {
    return Err(audio_error(pcm.Limit));
  };
  let Some(encoded) = state.encoded_frames +? 1i64 else {
    return Err(audio_error(pcm.Limit));
  };
  if (info.total_frames != 0u64 && position as u64 > info.total_frames) {
    return Err(audio_error(pcm.Header));
  }
  return Ok((position, encoded));
}

function output(own state: Input, own frame: flac.Frame): Result[(Input, ReadEvent), source.ReadError] {
  let Some(info) = state.info else {
    return Err(audio_error(pcm.Header));
  };
  let (position, encoded) = counted(state, frame, info)?;
  let hash = if (state.check_md5) { hash_frame(state.hash, frame) } else { state.hash };
  let audio = Audio { info: info, position: state.position, frames: frame.frames, samples: frame.samples };
  let next = consume(state, frame.bytes_read)?;
  return Ok((Input { ...next, stage: ReadFrame, needed: 6, position: position, encoded_frames: encoded, variable: frame.variable, block_size: if (encoded == 1i64) { frame.frames } else { next.block_size }, previous: frame.frames, hash: hash }, Output(Samples(audio))));
}

function decode(own state: Input): Result[(Input, ReadEvent), source.ReadError] {
  let Some(header) = state.header else {
    return Err(audio_error(pcm.Header));
  };
  let available = state.buffer.available();
  let size = if (available < ceiling(state)) { available } else { ceiling(state) };
  let frame = match (flac.decode_frame(state.buffer.view()[:size], 0, header, state.limits.audio)) { Ok(value) => value, Err(e) => { return retry(state, e); } };
  return output(state, frame);
}

pub function (own state: Input) step_with(read: (i32) => Result[u8[], IoError]): Result[(Input, ReadEvent), source.ReadError] {
  return match (state.stage) {
    StreamInfo => stream_info(state, read),
    MetadataHeader => metadata_header(state, read),
    SkipMetadata => skip_metadata(state, read),
    BeginTrack => begin(state),
    ReadFrame => read_frame(state, read),
    Decode => decode(state),
    Complete => Ok((state, Finished)),
  };
}

pub function (own state: Input) step(reader: Reader): Result[(Input, ReadEvent), source.ReadError] {
  return state.step_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}

pub function (own state: Input) next_with(read: (i32) => Result[u8[], IoError]): Result[(Input, Option[Event]), source.ReadError] {
  while (true) {
    match (state.step_with(read)) {
      Ok((next, Pending())) => {
        state = next;
      },
      Ok((next, Output(value))) => {
        return Ok((next, Some(value)));
      },
      Ok((next, Finished())) => {
        return Ok((next, None));
      },
      Err(e) => {
        return Err(e);
      }
    }
  }
}

pub function (own state: Input) next(reader: Reader): Result[(Input, Option[Event]), source.ReadError] {
  return state.next_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
