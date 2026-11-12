# Bounded MP4 video file indexing

This module indexes AVC, HEVC, VP9, AV1, and QTRLE tracks from a borrowed seekable source.
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
import "std/audio/source";
import "std/audio/mp4/source" as metadata;
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

pub function default_limits(): Limits {
  return Limits { index: mp4.default_limits(), input: source.Limits { chunk_size: 16384, max_buffer_bytes: 16777216 }, max_file_bytes: 68719476736i64, max_metadata_bytes: 16777216, max_operations: 131072 };
}

function source_error(error: source.ReadError): ReadError {
  return match (error) { source.ReadIo(value) => ReadIo(value), source.ReadAudio(value) => ReadVideo(mp4.video_error(value)), source.ReadSize => ReadVideo(frame.Limit) };
}

function source_limits(limits: Limits): metadata.Limits {
  return metadata.Limits { input: limits.input, max_file_bytes: limits.max_file_bytes, max_metadata_bytes: limits.max_metadata_bytes, max_input_bytes: limits.index.max_input_bytes, max_boxes: limits.index.max_boxes, max_operations: limits.max_operations };
}

pub function inspect_with(bytes: i64, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], limits: Limits): Result[mp4.Index, ReadError] {
  let configured = source_limits(limits);
  if let Err(error) = metadata.validate(bytes, configured) {
    return Err(source_error(error));
  }
  if let Err(error) = mp4.validate_limits([], limits.index) {
    return Err(ReadVideo(mp4.video_error(error)));
  }
  let movie = match (metadata.inspect_with(bytes, seek, read, configured)) { Ok(value) => value, Err(error) => { return Err(source_error(error)); } };
  let index = match (mp4.inspect_movie(movie.bytes, movie.media, movie.file_bytes, movie.top_boxes, limits.index)) { Ok(value) => value, Err(error) => { return Err(ReadVideo(error)); } };
  return match (mp4.check_file_type(index, movie.file_type)) { Ok(value) => Ok(value), Err(error) => Err(ReadVideo(error)) };
}

pub function inspect(reader: Reader, limits: Limits): Result[mp4.Index, ReadError] {
  if (limits.max_operations < 1) {
    return Err(ReadError.ReadVideo(frame.Limit));
  }
  let bytes = match (reader.seek(0i64, 2)) { Ok(value) => value, Err(error) => { return Err(ReadError.ReadIo(error)); } };
  return inspect_with(bytes, (offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), Limits { ...limits, max_operations: limits.max_operations - 1 });
}
```
