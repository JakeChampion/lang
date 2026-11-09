# CAF chunks from seekable sources

This scanner validates CAF chunk extents with signed 64-bit file offsets.
It reads only the file header and chunk headers.
Unknown payloads require no reads or retained storage.
Each step performs at most one seek, one bounded read, or one header parse.
The caller supplies the same immutable source and closes it after use.

```fern
<<*>>=
// Scan CAF chunk extents with bounded reads and signed 64-bit offsets.

import "std/audio/file";
import "std/audio/pcm";
import "std/audio/source";
import "std/audio/wire";

pub struct Range { start: i64, end: i64 }

pub struct File { description: Range, cookie: Option[Range], packets: Option[Range], channels: Option[Range], data: Range }

pub opaque struct Scanner { bytes: i64, maximum: i32, count: i32, phase: i32, window: Option[file.Window], input: source.Limits, description: Option[Range], cookie: Option[Range], packets: Option[Range], channels: Option[Range], data: Option[Range] }

pub enum Event { Pending, Ready(File), Finished }

function audio(e: pcm.AudioError): source.ReadError {
  return source.ReadAudio(e);
}

pub function create(bytes: i64, maximum: i32, input: source.Limits): Result[Scanner, source.ReadError] {
  if (maximum < 1 || input.max_buffer_bytes < 12) {
    return Err(audio(pcm.Limit));
  }
  if (bytes < 8i64) {
    return Err(audio(pcm.Truncated));
  }
  let window = file.create(0i64, 8, input)?;
  return Ok(Scanner { bytes: bytes, maximum: maximum, count: 0, phase: 0, window: Some(window), input: input, description: None, cookie: None, packets: None, channels: None, data: None });
}

function one(previous: Option[Range], current: Range): Result[Option[Range], source.ReadError] {
  if let Some(_) = previous {
    return Err(audio(pcm.Header));
  }
  return Ok(Some(current));
}

function kind(header: [u8]): i32 {
  if (wire.tag(header, 0, "desc")) {
    return 1;
  }
  if (wire.tag(header, 0, "kuki")) {
    return 2;
  }
  if (wire.tag(header, 0, "pakt")) {
    return 3;
  }
  if (wire.tag(header, 0, "chan")) {
    return 4;
  }
  if (wire.tag(header, 0, "data")) {
    return 5;
  }
  return 0;
}

function previous(scan: Scanner, kind: i32): Option[Range] {
  return match (kind) { 1 => scan.description, 2 => scan.cookie, 3 => scan.packets, 4 => scan.channels, _ => scan.data };
}

function record(own scan: Scanner, kind: i32, range: Range): Result[Scanner, source.ReadError] {
  if (scan.count == 0 && kind != 1) {
    return Err(audio(pcm.Header));
  }
  if (kind == 0) {
    return Ok(scan);
  }
  let value = one(previous(scan, kind), range)?;
  return Ok(match (kind) {
    1 => Scanner { ...scan, description: value },
    2 => Scanner { ...scan, cookie: value },
    3 => Scanner { ...scan, packets: value },
    4 => Scanner { ...scan, channels: value },
    _ => Scanner { ...scan, data: value },
  });
}

function finish(own scan: Scanner): Result[(Scanner, Event), source.ReadError] {
  let Some(description) = scan.description else {
    return Err(audio(pcm.Header));
  };
  let Some(data) = scan.data else {
    return Err(audio(pcm.Header));
  };
  if (data.end - data.start < 4i64) {
    return Err(audio(pcm.Truncated));
  }
  let result = File { description: description, cookie: scan.cookie, packets: scan.packets, channels: scan.channels, data: data };
  return Ok((Scanner { ...scan, phase: 2, window: None }, Ready(result)));
}

function next(own scan: Scanner, at: i64): Result[(Scanner, Event), source.ReadError] {
  if (at == scan.bytes) {
    return finish(scan);
  }
  if (scan.count >= scan.maximum) {
    return Err(audio(pcm.Limit));
  }
  if (at < 0i64 || at > scan.bytes || scan.bytes - at < 12i64) {
    return Err(audio(pcm.Truncated));
  }
  let window = file.create(at, 12, scan.input)?;
  return Ok((Scanner { ...scan, window: Some(window), phase: 1 }, Pending));
}

function file_header(header: [u8]): Result[void, source.ReadError] {
  if (!wire.tag(header, 0, "caff")) {
    return Err(audio(pcm.Header));
  }
  if (wire.be(header, 4, 2) != 1u64 || wire.be(header, 6, 2) != 0u64) {
    return Err(audio(pcm.Unsupported));
  }
  return Ok(());
}

function chunk_header(header: [u8], offset: i64, bytes: i64): Result[(i32, Range), source.ReadError] {
  let start = offset + 12i64;
  let size = wire.be(header, 4, 8) as i64;
  let tag = kind(header);
  let end = bytes;
  if (size < 0i64) {
    if (size != -1i64 || tag != 5) {
      return Err(audio(pcm.Header));
    }
  } else {
    if (size > bytes - start) {
      return Err(audio(pcm.Truncated));
    }
    end = start + size;
  }
  return Ok((tag, Range { start: start, end: end }));
}

function parse(own scan: Scanner): Result[(Scanner, Event), source.ReadError] {
  let Some(current) = scan.window else {
    return Err(audio(pcm.Header));
  };
  if (scan.phase == 0) {
    file_header(current.view())?;
    return next(scan, 8i64);
  }
  let (tag, range) = chunk_header(current.view(), current.offset, scan.bytes)?;
  let recorded = record(scan, tag, range)?;
  return next(Scanner { ...recorded, count: recorded.count + 1 }, range.end);
}

pub function (own scan: Scanner) step_with(seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Scanner, Event), source.ReadError] {
  let Scanner { bytes, maximum, count, phase, window, input, description, cookie, packets, channels, data } = scan;
  if (phase == 2) {
    return Ok((Scanner { bytes: bytes, maximum: maximum, count: count, phase: phase, window: None, input: input, description: description, cookie: cookie, packets: packets, channels: channels, data: data }, Finished));
  }
  let Some(current) = window else {
    return Err(audio(pcm.Header));
  };
  if (!current.ready) {
    let updated = current.step_with(seek, read)?;
    return Ok((Scanner { bytes: bytes, maximum: maximum, count: count, phase: phase, window: Some(updated), input: input, description: description, cookie: cookie, packets: packets, channels: channels, data: data }, Pending));
  }
  return parse(Scanner { bytes: bytes, maximum: maximum, count: count, phase: phase, window: Some(current), input: input, description: description, cookie: cookie, packets: packets, channels: channels, data: data });
}

pub function (own scan: Scanner) step(reader: Reader): Result[(Scanner, Event), source.ReadError] {
  return scan.step_with((at: i64): Result[i64, IoError] => reader.seek(at, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count));
}
```
