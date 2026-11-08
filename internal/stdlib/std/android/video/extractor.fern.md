# Android media extractor

This module reads media tracks and packets through the NDK MediaExtractor API.
The minimum API level is 28, for the sample-size query before a buffer write.
The source is a seekable file descriptor with an explicit byte range.
The caller retains the descriptor until the extractor closes.

A track format retains codec configuration and color metadata for MediaCodec.
Close each returned format once after use.
`read_into` writes directly into a decoder input slot without a Fern packet copy.
It leaves the extractor on the same sample. Advance after a successful submission.
Encrypted and partial samples return `Unsupported` before a buffer write.

```fern
<<*>>=
// This module extracts bounded media samples and seeks to sync samples on Android.
import "std/android/video";
import "std/video/frame";
import "core/cmp";

pub struct Extractor { handle: usize, tracks: i32, max_packet_bytes: i32 }

pub struct Sample { size: i32, presentation_time_us: i64, flags: u32, track: i32 }

@derive(cmp.Eq, cmp.Debug)
pub enum Seek { PreviousSync, NextSync, ClosestSync }

@import("libmediandk.so", "AMediaExtractor_new")
function c_new(): usize;

@import("libmediandk.so", "AMediaExtractor_delete")
function c_delete(extractor: usize): i32;

@import("libmediandk.so", "AMediaExtractor_setDataSourceFd")
function c_source(extractor: usize, fd: i32, offset: i64, length: i64): i32;

@import("libmediandk.so", "AMediaExtractor_getTrackCount")
function c_count(extractor: usize): usize;

@import("libmediandk.so", "AMediaExtractor_getTrackFormat")
function c_format(extractor: usize, track: usize): usize;

@import("libmediandk.so", "AMediaExtractor_selectTrack")
function c_select(extractor: usize, track: usize): i32;

@import("libmediandk.so", "AMediaExtractor_getSampleSize")
function c_size(extractor: usize): i64;

@import("libmediandk.so", "AMediaExtractor_getSampleTime")
function c_time(extractor: usize): i64;

@import("libmediandk.so", "AMediaExtractor_getSampleFlags")
function c_flags(extractor: usize): u32;

@import("libmediandk.so", "AMediaExtractor_getSampleTrackIndex")
function c_track(extractor: usize): i32;

@import("libmediandk.so", "AMediaExtractor_readSampleData")
function c_read(extractor: usize, data: usize, capacity: usize): i64;

@import("libmediandk.so", "AMediaExtractor_advance")
function c_advance(extractor: usize): u8;

@import("libmediandk.so", "AMediaExtractor_seekTo")
function c_seek(extractor: usize, presentation_time_us: i64, mode: i32): i32;

pub function open(fd: i32, offset: i64, length: i64, maximum: i32): Result[Extractor, frame.VideoError] {
  if (fd < 0 || offset < 0i64 || length <= 0i64) {
    return Err(frame.Header);
  }
  let Some(end) = offset +? length else {
    return Err(frame.Limit);
  };
  if (end < offset || maximum <= 0) {
    return Err(frame.Limit);
  }
  let handle = c_new();
  if (handle == 0 as usize) {
    return Err(frame.Platform);
  }
  if (c_source(handle, fd, offset, length) != 0) {
    let _ = c_delete(handle);
    return Err(frame.Platform);
  }
  let count = c_count(handle);
  if (count > 256 as usize) {
    let _ = c_delete(handle);
    return Err(frame.Limit);
  }
  return Ok(Extractor { handle: handle, tracks: count as i32, max_packet_bytes: maximum });
}

pub function (extractor: Extractor) close(): Result[void, frame.VideoError] {
  if (c_delete(extractor.handle) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function (extractor: Extractor) track_format(track: i32): Result[video.CodecFormat, frame.VideoError] {
  if (track < 0 || track >= extractor.tracks) {
    return Err(frame.Header);
  }
  let format = c_format(extractor.handle, track as usize);
  if (format == 0 as usize) {
    return Err(frame.Platform);
  }
  return Ok(video.CodecFormat { handle: format });
}

pub function (extractor: Extractor) select_track(track: i32): Result[void, frame.VideoError] {
  if (track < 0 || track >= extractor.tracks) {
    return Err(frame.Header);
  }
  if (c_select(extractor.handle, track as usize) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function (extractor: Extractor) read_into(slot: video.InputBuffer): Result[Option[Sample], frame.VideoError] {
  if (slot.index < 0 || slot.data == 0 as usize || slot.capacity < 0) {
    return Err(frame.Header);
  }
  let size = c_size(extractor.handle);
  if (size == -1i64) {
    return Ok(None);
  }
  if (size < 0i64) {
    return Err(frame.Platform);
  }
  if (size > extractor.max_packet_bytes as i64) {
    return Err(frame.Limit);
  }
  if (size > slot.capacity as i64) {
    return Err(frame.Buffer);
  }
  let sample = sample_header(extractor, size as i32)?;
  let copied = c_read(extractor.handle, slot.data, slot.capacity as usize);
  if (copied != size) {
    return Err(frame.Platform);
  }
  return Ok(Some(sample));
}

function sample_header(extractor: Extractor, size: i32): Result[Sample, frame.VideoError] {
  let flags = c_flags(extractor.handle);
  if ((flags & 4294967294u32) != 0u32) {
    return Err(frame.Unsupported);
  }
  let pts = c_time(extractor.handle);
  let track = c_track(extractor.handle);
  if (pts < 0i64 || track < 0 || track >= extractor.tracks || size == 0) {
    return Err(frame.Header);
  }
  return Ok(Sample { size: size, presentation_time_us: pts, flags: flags, track: track });
}

pub function (extractor: Extractor) advance(): boolean {
  return c_advance(extractor.handle) != 0u8;
}

pub function (extractor: Extractor) seek(presentation_time_us: i64, mode: Seek): Result[void, frame.VideoError] {
  if (presentation_time_us < 0i64) {
    return Err(frame.Header);
  }
  let value = match (mode) { PreviousSync => 0, NextSync => 1, ClosestSync => 2 };
  if (c_seek(extractor.handle, presentation_time_us, value) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}
```
