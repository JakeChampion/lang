# Bounded Annex B NAL input

This module reads AVC and HEVC NAL units from a borrowed sequential source.
It reuses the complete-view Annex B parser and the bounded byte-source buffer.
Each step performs at most one source read and emits at most one unit.
The scanner preserves its position across short reads. It does not rescan an incomplete payload after each read.

The caller owns and closes the source. An empty successful read means permanent EOF.
The same source must preserve its position across calls.
I/O errors remain separate from framing errors.

An empty source returns `End`. An incomplete prefix or missing payload returns `Truncated`.
Empty NAL units and invalid prefixes return `Header`.

Limits bound retained input-window bytes, unit payload bytes, unit count, and source-read operations.
The input window needs room for the payload, prefix, padding, and the next delimiter.
Creation requires at least eight bytes of window space beyond the unit limit.

Additional zero padding can exhaust the window before the unit limit.
Limits apply before an event copies its payload.

Each unit owns its bytes and preserves emulation-prevention bytes.
Its offset counts bytes from the source's initial position, with signed 64-bit arithmetic.
The buffer limit excludes retained events and does not define an exact process-memory bound.

These units are not necessarily complete codec access units.
The reader does not discover picture boundaries, assign timestamps, or decode pixels.
The caller must supply access-unit boundaries and timing before decoder submission or muxing.
The API does not make a blocking source asynchronous.
Run blocking I/O outside the Android loop callback.
After an error, discard the consumed state and close or restart the source.

```fern
<<*>>=
// Read Annex B NAL units through a bounded sequential byte source.
import "std/video/nal";
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

pub struct Limits { input: source.Limits, max_unit_bytes: i32, max_units: i32, max_operations: i32 }

pub struct Unit { offset: i64, bytes: u8[] }

pub enum Event { Waiting, UnitReady(Unit), End }

pub struct Stream { buffer: source.Buffer, limits: Limits, offset: i64, scan: i32, start: i32, zeros: i32, units: i32, operations: i32, done: boolean }

pub struct Step { stream: Stream, event: Event }

pub function default_limits(): Limits {
  return Limits { input: source.Limits { chunk_size: 16384, max_buffer_bytes: 16777224 }, max_unit_bytes: 16777216, max_units: 1048576, max_operations: 4194304 };
}

function source_error(error: source.ReadError): ReadError {
  return match (error) {
    source.ReadIo(value) => ReadIo(value),
    source.ReadAudio(value) => ReadVideo(match (value) { pcm.Truncated => frame.Truncated, pcm.Limit => frame.Limit, _ => frame.Header }),
    source.ReadSize => ReadVideo(frame.Limit),
  };
}

pub function create(limits: Limits): Result[Stream, ReadError] {
  if (limits.max_unit_bytes < 1 || limits.input.max_buffer_bytes < 9 || limits.max_unit_bytes > limits.input.max_buffer_bytes - 8 || limits.max_units < 0 || limits.max_operations < 1) {
    return Err(ReadVideo(frame.Limit));
  }
  let buffer = match (source.create(limits.input)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  return Ok(Stream { buffer: buffer, limits: limits, offset: 0i64, scan: 0, start: -1, zeros: 0, units: 0, operations: 0, done: false });
}

function copy(input: [u8]): u8[] {
  let bytes: u8[] = [];
  for byte in input {
    bytes = bytes.append(byte);
  }
  return bytes;
}

function emit(own stream: Stream): Result[Step, ReadError] {
  let limits = frame.Limits { max_pixels: 0, max_packet_bytes: stream.limits.input.max_buffer_bytes };
  let parsed = match (nal.next_annex_b(stream.buffer.view(), 0, limits)) {
    Ok(Some(value)) => value,
    Err(error) => { return Err(ReadVideo(error)); },
    _ => { return Err(ReadVideo(frame.State)); },
  };
  if (parsed.size > stream.limits.max_unit_bytes || stream.units >= stream.limits.max_units) {
    return Err(ReadVideo(frame.Limit));
  }
  let Some(at) = stream.offset +? parsed.offset as i64 else {
    return Err(ReadVideo(frame.Limit));
  };
  let Some(next) = stream.offset +? parsed.next_offset as i64 else {
    return Err(ReadVideo(frame.Limit));
  };
  let unit = Unit { offset: at, bytes: copy(stream.buffer.view()[parsed.offset:parsed.offset + parsed.size]) };
  let buffer = match (stream.buffer.advance(parsed.next_offset)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  return Ok(Step { stream: Stream { ...stream, buffer: buffer, offset: next, scan: 0, start: -1, zeros: 0, units: stream.units + 1 }, event: UnitReady(unit) });
}

function prefix(own stream: Stream): Result[Stream, ReadError] {
  let view = stream.buffer.view();
  let at = stream.scan;
  while (at < view.len() && view[at] == 0u8) {
    at += 1;
  }
  if (at == view.len()) {
    return Ok(Stream { ...stream, scan: at });
  }
  if (at < 2 || view[at] != 1u8) {
    return Err(ReadVideo(frame.Header));
  }
  return Ok(Stream { ...stream, scan: at + 1, start: at + 1, zeros: 0 });
}

function body(own stream: Stream): Result[Step, ReadError] {
  let view = stream.buffer.view();
  let at = stream.scan;
  let zeros = stream.zeros;
  while (at < view.len()) {
    let byte = view[at];
    if (byte == 1u8 && zeros >= 2) {
      return emit(stream);
    }
    if (byte == 0u8) {
      zeros += 1;
    } else {
      zeros = 0;
      if (at + 1 - stream.start > stream.limits.max_unit_bytes) {
        return Err(ReadVideo(frame.Limit));
      }
    }
    at += 1;
  }
  return Ok(Step { stream: Stream { ...stream, scan: at, zeros: zeros }, event: Waiting });
}

function inspect(own stream: Stream): Result[Step, ReadError] {
  if (stream.units >= stream.limits.max_units && stream.buffer.available() > 0) {
    return Err(ReadVideo(frame.Limit));
  }
  if (stream.start < 0) {
    stream = prefix(stream)?;
  }
  if (stream.start >= 0) {
    return body(stream);
  }
  return Ok(Step { stream: stream, event: Waiting });
}

function finish(own stream: Stream): Result[Step, ReadError] {
  if (stream.buffer.available() == 0) {
    return Ok(Step { stream: Stream { ...stream, done: true }, event: End });
  }
  return emit(stream);
}

function read_more(own buffer: source.Buffer, probe: boolean, read: (i32) => Result[u8[], IoError]): Result[source.Buffer, ReadError] {
  let limits = buffer.limits;
  let needed = buffer.available() + 1;
  let chunk = if (probe) { 1 } else { limits.chunk_size };
  let pending = source.Buffer { ...buffer, limits: source.Limits { ...limits, chunk_size: chunk } };
  let (buffer, _) = match (pending.ensure(needed, read)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  return Ok(source.Buffer { ...buffer, limits: limits });
}

function fill(own stream: Stream, read: (i32) => Result[u8[], IoError]): Result[Stream, ReadError] {
  if (stream.operations >= stream.limits.max_operations || stream.buffer.available() >= stream.limits.input.max_buffer_bytes) {
    return Err(ReadVideo(frame.Limit));
  }
  let probe = stream.units >= stream.limits.max_units;
  let buffer = read_more(stream.buffer, probe, read)?;
  return Ok(Stream { ...stream, buffer: buffer, operations: stream.operations + 1 });
}

pub function (own stream: Stream) step_with(read: (i32) => Result[u8[], IoError]): Result[Step, ReadError] {
  if (stream.done) {
    return Ok(Step { stream: stream, event: End });
  }
  let first = inspect(stream)?;
  match (first.event) {
    Waiting => {},
    _ => {
      return Ok(first);
    }
  }
  stream = first.stream;
  if (stream.buffer.eof) {
    return finish(stream);
  }
  let next = inspect(fill(stream, read)?)?;
  match (next.event) {
    Waiting => {},
    _ => {
      return Ok(next);
    }
  }
  stream = next.stream;
  if (stream.buffer.eof) {
    return finish(stream);
  }
  return Ok(Step { stream: stream, event: Waiting });
}

pub function (own stream: Stream) step(reader: Reader): Result[Step, ReadError] {
  return stream.step_with((count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
