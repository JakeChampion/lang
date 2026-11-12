# Bounded MP4 video file indexing

This module indexes AVC, HEVC, AV1, and QTRLE tracks from a borrowed seekable source.
It reads top-level box headers and one bounded movie metadata box.
It skips media payloads and other top-level payloads without a copy.
Packet offsets and the source length use signed 64-bit counts.

The caller owns and closes the source. The source must remain immutable through index and packet reads.
The callbacks must identify that same source and preserve its position between calls.
Short reads are permitted. An empty successful read before a window completes means truncated input.
I/O errors retain their own category.

The metadata, input-window, box, packet, file-length, and I/O-operation budgets remain explicit.
The nested index input-byte limit applies to the movie metadata box.

An I/O operation is one seek or one read. Short reads count as separate operations.
The returned index retains metadata and packet ranges, without encoded media payloads.
An index does not confirm the physical bytes of skipped payloads.
Packet reads must still check for truncation.

The calls are synchronous. Run them outside a latency-sensitive Android loop callback.

This API has the same codec, edit-list, encryption, and fragmentation limits as `std/video/mp4`.
It does not decode compressed pixels or apply edit lists.

```fern
<<*>>=
// Index bounded MP4 metadata through a borrowed seekable source.
import "std/video/mp4";
import "std/video/frame";
import "std/audio/mp4/boxes";
import "std/audio/mp4/tables";
import "std/audio/file";
import "std/audio/source";
import "std/audio/wire";
import "core/error";
import "core/cmp";

pub enum ReadError { ReadIo(IoError), ReadVideo(frame.VideoError) }

impl error.Error for ReadError {}

impl cmp.Display for ReadError {}

pub function (error: ReadError) message(): string {
  return match (error) { ReadIo(value) => value.message(), ReadVideo(value) => value.message() };
}

pub function (error: ReadError) to_string(): string {
  return error.message();
}

pub struct Limits { index: mp4.Limits, input: source.Limits, max_file_bytes: i64, max_metadata_bytes: i32, max_operations: i32 }

struct Scan { limits: Limits, file_bytes: i64, at: i64, boxes: i32, operations: i32, file_type: boolean, metadata: u8[], media: tables.Media[] }

struct Header { kind: u32, start: i64, data: i64, end: i64 }

pub function default_limits(): Limits {
  return Limits { index: mp4.default_limits(), input: source.Limits { chunk_size: 16384, max_buffer_bytes: 16777216 }, max_file_bytes: 68719476736i64, max_metadata_bytes: 16777216, max_operations: 131072 };
}

function source_error(error: source.ReadError): ReadError {
  return match (error) { source.ReadIo(value) => ReadIo(value), source.ReadAudio(value) => ReadVideo(mp4.video_error(value)), source.ReadSize => ReadVideo(frame.Limit) };
}

function create(bytes: i64, limits: Limits): Result[Scan, ReadError] {
  if (bytes < 8i64) {
    return Err(ReadVideo(frame.Truncated));
  }
  if (limits.max_file_bytes < 8i64 || bytes > limits.max_file_bytes || limits.max_metadata_bytes < 8 || limits.max_operations < 1) {
    return Err(ReadVideo(frame.Limit));
  }
  if let Err(error) = mp4.validate_limits([], limits.index) {
    return Err(ReadVideo(mp4.video_error(error)));
  }
  if let Err(error) = source.create(limits.input) {
    return Err(source_error(error));
  }
  return Ok(Scan { limits: limits, file_bytes: bytes, at: 0i64, boxes: 0, operations: 0, file_type: false, metadata: [], media: [] });
}

function load(scan: Scan, offset: i64, bytes: i32, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Scan, u8[]), ReadError] {
  let window = match (file.create(offset, bytes, scan.limits.input)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  let operations = scan.operations;
  while (!window.ready) {
    if (operations >= scan.limits.max_operations) {
      return Err(ReadVideo(frame.Limit));
    }
    operations += 1;
    window = match (window.step_with(seek, read)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  }
  return Ok((Scan { ...scan, operations: operations }, window.buffer.bytes));
}

function extended(scan: Scan, prefix: [u8], seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Scan, u64, i32), ReadError] {
  let size = wire.be(prefix, 0, 4);
  if (size == 0u64) {
    return Ok((scan, (scan.file_bytes - scan.at) as u64, 8));
  }
  if (size != 1u64) {
    return Ok((scan, size, 8));
  }
  if (scan.file_bytes - scan.at < 16i64) {
    return Err(ReadVideo(frame.Truncated));
  }
  let (next, bytes) = load(scan, scan.at + 8i64, 8, seek, read)?;
  return Ok((next, wire.be(bytes, 0, 8), 16));
}

function header(scan: Scan, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Scan, Header), ReadError] {
  if (scan.file_bytes - scan.at < 8i64) {
    return Err(ReadVideo(frame.Truncated));
  }
  let (next, prefix) = load(scan, scan.at, 8, seek, read)?;
  let (after, size, width) = extended(next, prefix, seek, read)?;
  let kind = wire.be(prefix, 4, 4) as u32;
  let bytes = width + (if (kind == boxes.tag("uuid")) { 16 } else { 0 });
  if (size < bytes as u64) {
    return Err(ReadVideo(frame.Header));
  }
  if (size > (scan.file_bytes - scan.at) as u64) {
    return Err(ReadVideo(frame.Truncated));
  }
  return Ok((after, Header { kind: kind, start: scan.at, data: scan.at + bytes as i64, end: scan.at + size as i64 }));
}

function retain(scan: Scan, box: Header, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[Scan, ReadError] {
  if (box.kind == boxes.tag("moof")) {
    return Err(ReadVideo(frame.Unsupported));
  }
  if (box.kind == boxes.tag("ftyp")) {
    if (scan.file_type || box.end - box.data < 8i64 || (box.end - box.data) % 4i64 != 0i64) {
      return Err(ReadVideo(frame.Header));
    }
    return Ok(Scan { ...scan, file_type: true });
  }
  if (box.kind == boxes.tag("mdat")) {
    return Ok(Scan { ...scan, media: scan.media.append(tables.Media { start: box.data, end: box.end }) });
  }
  if (box.kind != boxes.tag("moov")) {
    return Ok(scan);
  }
  return retain_movie(scan, box, seek, read);
}

function retain_movie(scan: Scan, box: Header, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[Scan, ReadError] {
  if (scan.metadata.len() != 0) {
    return Err(ReadVideo(frame.Header));
  }
  let bytes = box.end - box.start;
  if (bytes > scan.limits.max_metadata_bytes as i64 || bytes > scan.limits.index.max_input_bytes as i64) {
    return Err(ReadVideo(frame.Limit));
  }
  let (next, metadata) = load(scan, box.start, bytes as i32, seek, read)?;
  return Ok(Scan { ...next, metadata: metadata });
}

pub function inspect_with(bytes: i64, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], limits: Limits): Result[mp4.Index, ReadError] {
  let scan = create(bytes, limits)?;
  while (scan.at < scan.file_bytes) {
    if (scan.boxes >= limits.index.max_boxes) {
      return Err(ReadVideo(frame.Limit));
    }
    let (next, box) = header(scan, seek, read)?;
    let retained = retain(next, box, seek, read)?;
    scan = Scan { ...retained, at: box.end, boxes: retained.boxes + 1 };
  }
  if (scan.metadata.len() == 0) {
    return Err(ReadVideo(frame.Header));
  }
  return index_movie(scan);
}

function index_movie(scan: Scan): Result[mp4.Index, ReadError] {
  let index = match (mp4.inspect_movie(scan.metadata, scan.media, scan.file_bytes, scan.boxes, scan.limits.index)) { Ok(value) => value, Err(error) => { return Err(ReadVideo(error)); } };
  return match (mp4.check_file_type(index, scan.file_type)) { Ok(value) => Ok(value), Err(error) => Err(ReadVideo(error)) };
}

pub function inspect(reader: Reader, limits: Limits): Result[mp4.Index, ReadError] {
  if (limits.max_operations < 1) {
    return Err(ReadError.ReadVideo(frame.Limit));
  }
  let bytes = match (reader.seek(0i64, 2)) { Ok(value) => value, Err(error) => { return Err(ReadError.ReadIo(error)); } };
  return inspect_with(bytes, (offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), Limits { ...limits, max_operations: limits.max_operations - 1 });
}
```
