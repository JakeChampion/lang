# Bounded AAC MP4 file indexing

This module indexes self-contained AAC tracks from a borrowed seekable MP4 source.
It shares the bounded movie scanner with the native video file index.
The scanner reads top-level headers and one movie box. It skips encoded media payloads.
Track configuration, packet tables, clocks, edits, and byte ranges reuse the complete-view audio readers.

The caller owns the immutable source and closes it after use.
Short reads are permitted. Each seek and read counts against the operation budget.
I/O failures remain distinct from codec errors. File offsets and length use signed 64-bit counts.

The nested encoded-input limit applies to the movie metadata and to each packet.
The returned index retains scalar configurations and packet metadata, without encoded media or PCM.
Use `std/audio/file.Window` to read one packet through that same source.
Skipped physical payload bytes still require validation during packet reads.

The index does not decode audio or apply presentation edits.
The caller must retain AAC decoder state and apply the existing presentation plan for priming and padding.
Fragmented files, encryption, external data references, and unsupported AAC configurations retain the complete-view restrictions.
The calls are synchronous and belong outside a latency-sensitive Android callback.

```fern
<<*>>=
// Index bounded AAC movie metadata without a complete encoded-file copy.
import "std/audio/mp4";
import "std/audio/mp4/index" as container;
import "std/audio/mp4/source" as metadata;
import "std/audio/source";
import "std/audio/pcm";

pub struct Limits { index: mp4.Limits, input: source.Limits, max_file_bytes: i64, max_metadata_bytes: i32, max_operations: i32 }

pub function default_limits(): Limits {
  return Limits { index: mp4.default_limits(), input: source.Limits { chunk_size: 16384, max_buffer_bytes: 16777216 }, max_file_bytes: 68719476736i64, max_metadata_bytes: 16777216, max_operations: 131072 };
}

function source_limits(limits: Limits): metadata.Limits {
  return metadata.Limits { input: limits.input, max_file_bytes: limits.max_file_bytes, max_metadata_bytes: limits.max_metadata_bytes, max_input_bytes: limits.index.audio.max_input_bytes, max_boxes: limits.index.max_boxes, max_operations: limits.max_operations };
}

pub function inspect_with(bytes: i64, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], limits: Limits): Result[mp4.Index, source.ReadError] {
  let configured = source_limits(limits);
  metadata.validate(bytes, configured)?;
  if let Err(error) = container.validate_limits([], limits.index) {
    return Err(source.ReadAudio(error));
  }
  let movie = metadata.inspect_with(bytes, seek, read, configured)?;
  if (!movie.file_type) {
    return Err(source.ReadAudio(pcm.Header));
  }
  return match (mp4.inspect_movie(movie.bytes, movie.media, movie.file_bytes, movie.top_boxes, limits.index)) { Ok(value) => Ok(value), Err(error) => Err(source.ReadAudio(error)) };
}

pub function inspect(reader: Reader, limits: Limits): Result[mp4.Index, source.ReadError] {
  if (limits.max_operations < 1) {
    return Err(source.ReadAudio(pcm.Limit));
  }
  let bytes = match (reader.seek(0i64, 2)) { Ok(value) => value, Err(error) => { return Err(source.ReadIo(error)); } };
  return inspect_with(bytes, (offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), Limits { ...limits, max_operations: limits.max_operations - 1 });
}
```
