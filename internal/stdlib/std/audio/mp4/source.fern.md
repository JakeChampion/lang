# Borrowed ISO movie metadata

This package helper supplies the shared file scanner for native AAC and video MP4 indexes.
It reads top-level headers and one bounded movie box while it skips media payloads.
The codec adapters retain their own track, packet, configuration, and file-type checks.
The caller owns the immutable source. Each seek and each short read counts against the operation budget.

```fern
<<*>>=
// Share bounded ISO file metadata reads across the audio and video adapters.
import "std/audio/mp4/boxes";
import "std/audio/mp4/tables";
import "std/audio/file";
import "std/audio/source";
import "std/audio/pcm";
import "std/audio/wire";

pub(package) struct Limits { input: source.Limits, max_file_bytes: i64, max_metadata_bytes: i32, max_input_bytes: i32, max_boxes: i32, max_operations: i32 }

pub(package) struct Metadata { bytes: u8[], media: tables.Media[], file_bytes: i64, top_boxes: i32, file_type: boolean }

struct Scan { limits: Limits, file_bytes: i64, at: i64, boxes: i32, operations: i32, file_type: boolean, metadata: u8[], media: tables.Media[] }

struct Header { kind: u32, start: i64, data: i64, end: i64 }

pub(package) function validate(bytes: i64, limits: Limits): Result[void, source.ReadError] {
  if (bytes < 8i64) {
    return Err(source.ReadAudio(pcm.Truncated));
  }
  if (limits.max_file_bytes < 8i64 || bytes > limits.max_file_bytes || limits.max_metadata_bytes < 8 || limits.max_operations < 1) {
    return Err(source.ReadAudio(pcm.Limit));
  }
  return Ok(());
}

function create(bytes: i64, limits: Limits): Result[Scan, source.ReadError] {
  validate(bytes, limits)?;
  if let Err(error) = source.create(limits.input) {
    return Err(error);
  }
  return Ok(Scan { limits: limits, file_bytes: bytes, at: 0i64, boxes: 0, operations: 0, file_type: false, metadata: [], media: [] });
}

function load(scan: Scan, offset: i64, bytes: i32, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Scan, u8[]), source.ReadError] {
  let window = match (file.create(offset, bytes, scan.limits.input)) { Ok(value) => value, Err(error) => { return Err(error); } };
  let operations = scan.operations;
  while (!window.ready) {
    if (operations >= scan.limits.max_operations) {
      return Err(source.ReadAudio(pcm.Limit));
    }
    operations += 1;
    window = match (window.step_with(seek, read)) { Ok(value) => value, Err(error) => { return Err(error); } };
  }
  return Ok((Scan { ...scan, operations: operations }, window.buffer.bytes));
}

function extended(scan: Scan, prefix: [u8], seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Scan, u64, i32), source.ReadError] {
  let size = wire.be(prefix, 0, 4);
  if (size == 0u64) {
    return Ok((scan, (scan.file_bytes - scan.at) as u64, 8));
  }
  if (size != 1u64) {
    return Ok((scan, size, 8));
  }
  if (scan.file_bytes - scan.at < 16i64) {
    return Err(source.ReadAudio(pcm.Truncated));
  }
  let (next, bytes) = load(scan, scan.at + 8i64, 8, seek, read)?;
  return Ok((next, wire.be(bytes, 0, 8), 16));
}

function header(scan: Scan, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[(Scan, Header), source.ReadError] {
  if (scan.file_bytes - scan.at < 8i64) {
    return Err(source.ReadAudio(pcm.Truncated));
  }
  let (next, prefix) = load(scan, scan.at, 8, seek, read)?;
  let (after, size, width) = extended(next, prefix, seek, read)?;
  let kind = wire.be(prefix, 4, 4) as u32;
  let bytes = width + (if (kind == boxes.tag("uuid")) { 16 } else { 0 });
  if (size < bytes as u64) {
    return Err(source.ReadAudio(pcm.Header));
  }
  if (size > (scan.file_bytes - scan.at) as u64) {
    return Err(source.ReadAudio(pcm.Truncated));
  }
  return Ok((after, Header { kind: kind, start: scan.at, data: scan.at + bytes as i64, end: scan.at + size as i64 }));
}

function retain(scan: Scan, box: Header, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[Scan, source.ReadError] {
  if (box.kind == boxes.tag("moof")) {
    return Err(source.ReadAudio(pcm.Unsupported));
  }
  if (box.kind == boxes.tag("ftyp")) {
    if (scan.file_type || box.end - box.data < 8i64 || (box.end - box.data) % 4i64 != 0i64) {
      return Err(source.ReadAudio(pcm.Header));
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

function retain_movie(scan: Scan, box: Header, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError]): Result[Scan, source.ReadError] {
  if (scan.metadata.len() != 0) {
    return Err(source.ReadAudio(pcm.Header));
  }
  let bytes = box.end - box.start;
  if (bytes > scan.limits.max_metadata_bytes as i64 || bytes > scan.limits.max_input_bytes as i64) {
    return Err(source.ReadAudio(pcm.Limit));
  }
  let (next, metadata) = load(scan, box.start, bytes as i32, seek, read)?;
  return Ok(Scan { ...next, metadata: metadata });
}

pub(package) function inspect_with(bytes: i64, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], limits: Limits): Result[Metadata, source.ReadError] {
  let scan = create(bytes, limits)?;
  while (scan.at < scan.file_bytes) {
    if (scan.boxes >= limits.max_boxes) {
      return Err(source.ReadAudio(pcm.Limit));
    }
    let (next, box) = header(scan, seek, read)?;
    let retained = retain(next, box, seek, read)?;
    scan = Scan { ...retained, at: box.end, boxes: retained.boxes + 1 };
  }
  if (scan.metadata.len() == 0) {
    return Err(source.ReadAudio(pcm.Header));
  }
  return Ok(Metadata { bytes: scan.metadata, media: scan.media, file_bytes: scan.file_bytes, top_boxes: scan.boxes, file_type: scan.file_type });
}
```
