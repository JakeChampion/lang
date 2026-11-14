# Bounded AV1 Annex B input

This module reads AV1 Annex B temporal units from a sequential byte source.
Each step performs at most one source read and emits at most one event.
It reuses the bounded source buffer, LEB128 reader, and native Annex B range parser.

Each packet owns its original encoded bytes, including the temporal-unit length field.
Its offset counts bytes from the start of the source with signed 64-bit arithmetic.
The packet's scalar ranges refer to its own bytes. Add the packet offset for absolute source ranges.

Separate limits bound input bytes, encoded packet bytes, frame and OBU records, temporal units, and source-read operations.
The buffer holds at most one pending temporal unit plus a short header read.
Retained output packets are separate. These limits do not define a process-memory bound.

An empty successful read means permanent EOF. EOF between temporal units returns `End`.
EOF inside a length or body returns `Truncated`. I/O errors retain their own category.
After an error, discard the consumed state and close or restart the source.

The caller owns and closes the source. Blocking I/O belongs outside the Android loop callback.
This reader does not validate complete frame payloads, assign timestamps, decode pixels, or control Android playback.

```fern
<<*>>=
// Read bounded AV1 Annex B packets through a sequential source.
import "std/video/av1";
import "std/video/av1/annex_b";
import "std/video/frame";
import "std/audio/source";
import "std/audio/pcm";
import "core/cmp";
import "core/error";

pub enum ReadError { ReadIo(IoError), ReadVideo(frame.VideoError) }

impl error.Error for ReadError {}

impl cmp.Display for ReadError {}

pub function (value: ReadError) message(): string {
  return match (value) { ReadIo(error) => error.message(), ReadVideo(error) => error.message() };
}

pub function (value: ReadError) to_string(): string {
  return value.message();
}

pub struct Limits { annex: annex_b.Limits, input: source.Limits, max_temporal_units: i32, max_operations: i32 }

pub struct Packet { offset: i64, bytes: u8[], unit: annex_b.TemporalUnit }

pub enum Event { Waiting, PacketReady(Packet), End }

pub enum Phase { Header, Body(i32), Done }

pub struct Stream { buffer: source.Buffer, phase: Phase, offset: i64, units: i32, operations: i32, limits: Limits }

pub struct Step { stream: Stream, event: Event }

pub function default_limits(): Limits {
  return Limits { annex: annex_b.default_limits(), input: source.Limits { chunk_size: 16384, max_buffer_bytes: 16777216 }, max_temporal_units: 1048576, max_operations: 4194304 };
}

function source_error(value: source.ReadError): ReadError {
  return match (value) {
    source.ReadIo(error) => ReadIo(error),
    source.ReadAudio(error) => ReadVideo(match (error) { pcm.Truncated => frame.Truncated, pcm.Limit => frame.Limit, _ => frame.Header }),
    source.ReadSize => ReadVideo(frame.Limit),
  };
}

function valid(limits: Limits): boolean {
  return limits.annex.video.max_pixels >= 0 && limits.annex.video.max_packet_bytes > 0 && limits.annex.max_frame_units >= 0 && limits.annex.max_obus >= 0 && limits.max_temporal_units >= 0 && limits.max_operations > 0;
}

pub function create(limits: Limits): Result[Stream, ReadError] {
  if (!valid(limits)) {
    return Err(ReadVideo(frame.Limit));
  }
  let buffer = match (source.create(limits.input)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  return Ok(Stream { buffer: buffer, phase: Header, offset: 0i64, units: 0, operations: 0, limits: limits });
}

function exact_read(own buffer: source.Buffer, needed: i32, read: (i32) => Result[u8[], IoError]): Result[source.Buffer, ReadError] {
  let original = buffer.limits;
  let missing = needed - buffer.available();
  let count = if (original.chunk_size < missing) { original.chunk_size } else { missing };
  let pending = source.Buffer { ...buffer, limits: source.Limits { ...original, chunk_size: count } };
  let (buffer, _) = match (pending.ensure(needed, read)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  return Ok(source.Buffer { ...buffer, limits: original });
}

function fill(own stream: Stream, needed: i32, read: (i32) => Result[u8[], IoError]): Result[Stream, ReadError] {
  if (stream.buffer.available() >= needed || stream.buffer.eof) {
    return Ok(stream);
  }
  if (stream.operations >= stream.limits.max_operations) {
    return Err(ReadVideo(frame.Limit));
  }
  let buffer = exact_read(stream.buffer, needed, read)?;
  return Ok(Stream { ...stream, buffer: buffer, operations: stream.operations + 1 });
}

function prefix(input: [u8]): i32 {
  let count = if (input.len() < 8) { input.len() } else { 8 };
  for i in 0..count {
    if ((input[i] & 128u8) == 0u8) {
      return i + 1;
    }
  }
  return if (count == 8) { -1 } else { 0 };
}

function incomplete_header(own stream: Stream): Result[Step, ReadError] {
  if (stream.buffer.eof) {
    if (stream.buffer.available() == 0) {
      return Ok(Step { stream: Stream { ...stream, phase: Done }, event: End });
    }
    return Err(ReadVideo(frame.Truncated));
  }
  if (stream.buffer.available() == stream.limits.input.max_buffer_bytes) {
    return Err(ReadVideo(frame.Limit));
  }
  return Ok(Step { stream: stream, event: Waiting });
}

function packet_bytes(stream: Stream, width: i32, size: u64): Result[i32, ReadError] {
  let maximum = stream.limits.annex.video.max_packet_bytes;
  let window = stream.limits.input.max_buffer_bytes;
  if (width > maximum || width > window || stream.units >= stream.limits.max_temporal_units) {
    return Err(ReadVideo(frame.Limit));
  }
  if (size > (maximum - width) as u64 || size > (window - width) as u64) {
    return Err(ReadVideo(frame.Limit));
  }
  let count = width + size as i32;
  let Some(_) = stream.offset +? count as i64 else {
    return Err(ReadVideo(frame.Limit));
  };
  return Ok(count);
}

function header(own stream: Stream, read: (i32) => Result[u8[], IoError]): Result[Step, ReadError] {
  let width = prefix(stream.buffer.view());
  if (width == 0 && !stream.buffer.eof) {
    let count = if (stream.limits.input.max_buffer_bytes < 8) { stream.limits.input.max_buffer_bytes } else { 8 };
    stream = fill(stream, count, read)?;
    width = prefix(stream.buffer.view());
  }
  if (width < 0) {
    return Err(ReadVideo(frame.Header));
  }
  if (width == 0) {
    return incomplete_header(stream);
  }
  let (_, size) = match (av1.leb128(stream.buffer.view()[:width], 0)) { Ok(value) => value, Err(error) => { return Err(ReadVideo(error)); } };
  let bytes = packet_bytes(stream, width, size)?;
  return Ok(Step { stream: Stream { ...stream, phase: Body(bytes) }, event: Waiting });
}

function copy(input: [u8]): u8[] {
  let bytes: u8[] = [];
  for byte in input {
    bytes = bytes.append(byte);
  }
  return bytes;
}

function body(own stream: Stream, count: i32, read: (i32) => Result[u8[], IoError]): Result[Step, ReadError] {
  stream = fill(stream, count, read)?;
  if (stream.buffer.available() < count) {
    return if (stream.buffer.eof) { Err(ReadVideo(frame.Truncated)) } else { Ok(Step { stream: stream, event: Waiting }) };
  }
  let unit = match (annex_b.next_temporal_unit(stream.buffer.view()[:count], 0, stream.limits.annex)) {
    Ok(Some(value)) => value,
    Err(error) => { return Err(ReadVideo(error)); },
    _ => { return Err(ReadVideo(frame.State)); },
  };
  let packet = Packet { offset: stream.offset, bytes: copy(stream.buffer.view()[:count]), unit: unit };
  let buffer = match (stream.buffer.advance(count)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  return Ok(Step { stream: Stream { ...stream, buffer: buffer, offset: stream.offset + count as i64, units: stream.units + 1, phase: Header }, event: PacketReady(packet) });
}

pub function (own stream: Stream) step_with(read: (i32) => Result[u8[], IoError]): Result[Step, ReadError] {
  return match (stream.phase) { Header => header(stream, read), Body(count) => body(stream, count, read), Done => Ok(Step { stream: stream, event: End }) };
}

pub function (own stream: Stream) step(reader: Reader): Result[Step, ReadError] {
  return stream.step_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
