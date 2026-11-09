# Bounded IVF packet input

This module reads VP8, VP9, and AV1 IVF packets from a borrowed sequential source.
It reuses the IVF header and packet parsers. It does not decode compressed pixels.
Each step performs at most one bounded source read and emits at most one event.
Short reads can return `Waiting` before the next event.

The API does not make a blocking source asynchronous.
Run blocking I/O outside the Android loop callback.

The caller owns and closes the source. The source must preserve its position between calls.
An empty successful read means permanent EOF.
EOF between packets returns `End`. EOF inside a header or packet returns `Truncated`.
I/O errors retain their own category.

Limits bound input-buffer bytes, individual packet bytes, packet count, and source-read operations.
Packet payload offsets use signed 64-bit counts from the start of the IVF source.
The header frame count remains advisory. It does not replace the packet-count limit.
The parser returns timestamps without conversion or a requirement for monotonic order.

Each packet event owns its encoded bytes. The input buffer owns the pending header or packet.
The buffer limit excludes retained packet events and does not define an exact process-memory bound.
After an error, discard the consumed state. The caller can close the source or restart it at a known boundary.

```fern
<<*>>=
// Read IVF packets through a bounded sequential byte source.
import "std/video/ivf";
import "std/video/frame";
import "std/audio/source";
import "std/audio/wire";
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

pub struct Limits { video: frame.Limits, input: source.Limits, max_packets: i32, max_operations: i32 }

pub struct Packet { offset: i64, timestamp: u64, bytes: u8[] }

pub enum Event { Waiting, Header(ivf.Header), PacketReady(Packet), End }

pub enum Phase { FileHeader, PacketHeader, PacketBody(i32), Done }

pub struct Stream { buffer: source.Buffer, phase: Phase, offset: i64, packets: i32, operations: i32, limits: Limits }

pub struct Step { stream: Stream, event: Event }

pub function default_limits(): Limits {
  return Limits { video: frame.default_limits(), input: source.Limits { chunk_size: 16384, max_buffer_bytes: 16777228 }, max_packets: 1048576, max_operations: 4194304 };
}

function source_error(value: source.ReadError): ReadError {
  return match (value) {
    source.ReadIo(error) => ReadIo(error),
    source.ReadAudio(error) => ReadVideo(match (error) { pcm.Truncated => frame.Truncated, pcm.Limit => frame.Limit, _ => frame.Header }),
    source.ReadSize => ReadVideo(frame.Limit),
  };
}

pub function create(limits: Limits): Result[Stream, ReadError] {
  if (limits.video.max_pixels < 0 || limits.video.max_packet_bytes < 1 || limits.input.max_buffer_bytes < 32 || limits.max_packets < 0 || limits.max_operations < 1) {
    return Err(ReadVideo(frame.Limit));
  }
  let buffer = match (source.create(limits.input)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  return Ok(Stream { buffer: buffer, phase: FileHeader, offset: 0i64, packets: 0, operations: 0, limits: limits });
}

function exact_read(own buffer: source.Buffer, bytes: i32, read: (i32) => Result[u8[], IoError]): Result[source.Buffer, ReadError] {
  let limits = buffer.limits;
  let count = bytes - buffer.available();
  let chunk = if (limits.chunk_size < count) { limits.chunk_size } else { count };
  let pending = source.Buffer { ...buffer, limits: source.Limits { ...limits, chunk_size: chunk } };
  let (buffer, _) = match (pending.ensure(bytes, read)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  return Ok(source.Buffer { ...buffer, limits: limits });
}

function fill(own stream: Stream, bytes: i32, read: (i32) => Result[u8[], IoError]): Result[Stream, ReadError] {
  if (stream.buffer.available() >= bytes || stream.buffer.eof) {
    return Ok(stream);
  }
  if (stream.operations >= stream.limits.max_operations) {
    return Err(ReadVideo(frame.Limit));
  }
  let buffer = exact_read(stream.buffer, bytes, read)?;
  return Ok(Stream { ...stream, buffer: buffer, operations: stream.operations + 1 });
}

function waiting(stream: Stream): Result[Step, ReadError] {
  if (stream.buffer.eof) {
    return Err(ReadVideo(frame.Truncated));
  }
  return Ok(Step { stream: stream, event: Waiting });
}

function advance(own stream: Stream, bytes: i32, phase: Phase): Result[Stream, ReadError] {
  let Some(offset) = stream.offset +? bytes as i64 else {
    return Err(ReadVideo(frame.Limit));
  };
  let buffer = match (stream.buffer.advance(bytes)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  return Ok(Stream { ...stream, buffer: buffer, offset: offset, phase: phase });
}

function header(own stream: Stream, read: (i32) => Result[u8[], IoError]): Result[Step, ReadError] {
  stream = fill(stream, 32, read)?;
  if (stream.buffer.available() < 32) {
    return waiting(stream);
  }
  let value = match (ivf.inspect(stream.buffer.view()[:32], stream.limits.video)) { Ok(value) => value, Err(error) => { return Err(ReadVideo(error)); } };
  return Ok(Step { stream: advance(stream, 32, PacketHeader)?, event: Header(value) });
}

function packet_size(stream: Stream): Result[i32, ReadError] {
  let size = wire.le(stream.buffer.view(), 0, 4);
  if (size == 0u64) {
    return Err(ReadVideo(frame.Header));
  }
  if (size > stream.limits.video.max_packet_bytes as u64 || size > (stream.limits.input.max_buffer_bytes - 12) as u64 || stream.packets >= stream.limits.max_packets) {
    return Err(ReadVideo(frame.Limit));
  }
  let Some(_) = stream.offset +? (size as i64 + 12i64) else {
    return Err(ReadVideo(frame.Limit));
  };
  return Ok(size as i32 + 12);
}

function packet_header(own stream: Stream, read: (i32) => Result[u8[], IoError]): Result[Step, ReadError] {
  stream = fill(stream, 12, read)?;
  if (stream.buffer.eof && stream.buffer.available() == 0) {
    return Ok(Step { stream: Stream { ...stream, phase: Done }, event: End });
  }
  if (stream.buffer.available() < 12) {
    return waiting(stream);
  }
  let bytes = packet_size(stream)?;
  return Ok(Step { stream: Stream { ...stream, phase: PacketBody(bytes) }, event: Waiting });
}

function copy(input: [u8]): u8[] {
  let bytes: u8[] = [];
  for byte in input {
    bytes = bytes.append(byte);
  }
  return bytes;
}

function packet_body(own stream: Stream, bytes: i32, read: (i32) => Result[u8[], IoError]): Result[Step, ReadError] {
  stream = fill(stream, bytes, read)?;
  if (stream.buffer.available() < bytes) {
    return waiting(stream);
  }
  let parsed = match (ivf.next_packet(stream.buffer.view()[:bytes], 0, stream.limits.video)) {
    Ok(Some(value)) => value,
    Err(error) => { return Err(ReadVideo(error)); },
    _ => { return Err(ReadVideo(frame.State)); },
  };
  let packet = Packet { offset: stream.offset + 12i64, timestamp: parsed.timestamp, bytes: copy(stream.buffer.view()[parsed.offset:parsed.next_offset]) };
  let next = advance(stream, bytes, PacketHeader)?;
  return Ok(Step { stream: Stream { ...next, packets: next.packets + 1 }, event: PacketReady(packet) });
}

pub function (own stream: Stream) step_with(read: (i32) => Result[u8[], IoError]): Result[Step, ReadError] {
  return match (stream.phase) {
    FileHeader => header(stream, read),
    PacketHeader => packet_header(stream, read),
    PacketBody(bytes) => packet_body(stream, bytes, read),
    Done => Ok(Step { stream: stream, event: End }),
  };
}

pub function (own stream: Stream) step(reader: Reader): Result[Step, ReadError] {
  return stream.step_with((bytes: i32): Result[u8[], IoError] => reader.read_chunk_bytes(bytes));
}
```
