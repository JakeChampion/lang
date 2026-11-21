# Bounded AAC and ALAC MP4 file indexing

This module indexes self-contained AAC tracks from a borrowed seekable MP4 source.
`inspect_alac_with` indexes ALAC tracks from the same source in the same way.
It shares the bounded movie scanner with the native video file index.
The scanner reads top-level headers and one movie box. It skips encoded media payloads.
Track configuration, packet tables, clocks, edits, and byte ranges reuse the complete-view audio readers.

The caller owns the immutable source and closes it after use.
Short reads are permitted. Each seek and read counts against the operation budget.
I/O failures remain distinct from codec errors. File offsets and length use signed 64-bit counts.

The nested encoded-input limit applies to the movie metadata and to each packet.
The returned index retains scalar configurations and packet metadata, without encoded media or PCM.
Use `inspect_alac` and `read_alac_packet_into` to read ALAC packets into reusable integer PCM storage from that same Reader.
Skipped physical payload bytes still require validation during packet reads.

The index does not decode audio or apply presentation edits.
The caller must retain AAC decoder state and apply the existing presentation plan for priming and padding.
Fragmented files, encryption, external data references, and unsupported AAC configurations retain the complete-view restrictions.
The calls are synchronous and belong outside a latency-sensitive Android callback.

```fern
<<*>>=
// Index bounded AAC movie metadata without a complete encoded-file copy.
import "std/audio/alac" as codec;
import "std/audio/file" as packet_file;
import "std/audio/mp4";
import "std/audio/mp4/alac";
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

function movie_with(bytes: i64, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], limits: Limits): Result[metadata.Metadata, source.ReadError] {
  let configured = source_limits(limits);
  metadata.validate(bytes, configured)?;
  if let Err(error) = container.validate_limits([], limits.index) {
    return Err(source.ReadAudio(error));
  }
  let movie = metadata.inspect_with(bytes, seek, read, configured)?;
  if (!movie.file_type) {
    return Err(source.ReadAudio(pcm.Header));
  }
  return Ok(movie);
}

pub function inspect_with(bytes: i64, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], limits: Limits): Result[mp4.Index, source.ReadError] {
  let movie = movie_with(bytes, seek, read, limits)?;
  return match (mp4.inspect_movie(movie.bytes, movie.media, movie.file_bytes, movie.top_boxes, limits.index)) { Ok(value) => Ok(value), Err(error) => Err(source.ReadAudio(error)) };
}

// The index of the ALAC tracks of a file of `bytes` bytes that `seek` and
// `read` give.
pub function inspect_alac_with(bytes: i64, seek: (i64) => Result[i64, IoError], read: (i32) => Result[u8[], IoError], limits: Limits): Result[alac.Index, source.ReadError] {
  let movie = movie_with(bytes, seek, read, limits)?;
  return match (alac.inspect_movie(movie.bytes, movie.media, movie.file_bytes, movie.top_boxes, limits.index)) { Ok(value) => Ok(value), Err(error) => Err(source.ReadAudio(error)) };
}

function reader_size(reader: Reader, limits: Limits): Result[i64, source.ReadError] {
  if (limits.max_operations < 1) {
    return Err(source.ReadAudio(pcm.Limit));
  }
  return match (reader.seek(0i64, 2)) { Ok(value) => Ok(value), Err(error) => Err(source.ReadIo(error)) };
}

pub function inspect(reader: Reader, limits: Limits): Result[mp4.Index, source.ReadError] {
  let bytes = reader_size(reader, limits)?;
  return inspect_with(bytes, (offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), Limits { ...limits, max_operations: limits.max_operations - 1 });
}

pub function inspect_alac(reader: Reader, limits: Limits): Result[alac.Index, source.ReadError] {
  let bytes = reader_size(reader, limits)?;
  return inspect_alac_with(bytes, (offset: i64): Result[i64, IoError] => reader.seek(offset, 0), (count: i32): Result[u8[], IoError] => reader.read_chunk_bytes(count), Limits { ...limits, max_operations: limits.max_operations - 1 });
}

// Read a single ALAC packet by its indexed ordinal. The source remains
// borrowed and presentation edits do not change the packet numbering.
pub function read_alac_packet_into(reader: Reader, own stream: alac.Stream, number: i32, own output: i32[], limits: Limits): Result[(alac.Stream, codec.Frame), source.ReadError] {
  let packet = match (stream.packet(number)) { Ok(p) => p, Err(e) => { return Err(source.ReadAudio(e)); } };
  if (packet.bytes > limits.index.audio.max_input_bytes || limits.index.audio.max_samples < 0 || limits.max_file_bytes < packet.bytes as i64 || packet.offset > limits.max_file_bytes - packet.bytes as i64) {
    return Err(source.ReadAudio(pcm.Limit));
  }
  let window = packet_file.create(packet.offset, packet.bytes, limits.input)?;
  let remaining = limits.max_operations;
  while (!window.ready) {
    if (remaining <= 0) { return Err(source.ReadAudio(pcm.Limit)); }
    remaining -= 1;
    window = window.step(reader)?;
  }
  return match (stream.decode_bytes_into(window.view(), output, limits.index.audio)) { Ok(decoded) => Ok(decoded), Err(e) => Err(source.ReadAudio(e)) };
}
```
