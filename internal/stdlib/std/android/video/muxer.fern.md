# Android video muxer

This module writes MP4 or WebM through the NDK MediaMuxer API.
It requires a writable, seekable file descriptor and Android API 21 or later.
The caller retains and closes the descriptor after the muxer closes.
Add tracks from encoder output formats before the first start.
A muxer cannot restart after a stop.

`write_view` accepts a live encoder output view without a Fern packet copy.
Write the view before the encoder releases its output buffer.
Codec configuration buffers belong in the track format and are not media samples.
Track timestamps use microseconds and must follow the container's ordering rules.

```fern
<<*>>=
// This module muxes codec output buffers into Android MP4 or WebM files.
import "std/android/video";
import "std/video/frame";
import "core/cmp";

@derive(cmp.Eq, cmp.Debug)
pub enum Container { MP4, WebM }

@derive(cmp.Eq, cmp.Debug)
pub enum State { Configuring, Started, Stopped }

pub struct Muxer { handle: usize, state: usize, scratch: usize, max_packet_bytes: i32 }

@import("libmediandk.so", "AMediaMuxer_new")
function c_new(fd: i32, format: i32): usize;

@import("libmediandk.so", "AMediaMuxer_delete")
function c_delete(muxer: usize): i32;

@import("libmediandk.so", "AMediaMuxer_addTrack")
function c_add(muxer: usize, format: usize): i64;

@import("libmediandk.so", "AMediaMuxer_start")
function c_start(muxer: usize): i32;

@import("libmediandk.so", "AMediaMuxer_stop")
function c_stop(muxer: usize): i32;

@import("libmediandk.so", "AMediaMuxer_writeSampleData")
function c_write(muxer: usize, track: usize, data: usize, info: usize): i32;

@import("libmediandk.so", "AMediaMuxer_setOrientationHint")
function c_orientation(muxer: usize, degrees: i32): i32;

pub function create(fd: i32, container: Container, maximum: i32): Result[Muxer, frame.VideoError] {
  if (fd < 0) {
    return Err(frame.Header);
  }
  if (maximum <= 0) {
    return Err(frame.Limit);
  }
  let kind = match (container) { MP4 => 0, WebM => 1 };
  let handle = c_new(fd, kind);
  if (handle == 0 as usize) {
    return Err(frame.Platform);
  }
  unsafe {
    let state = __alloc(8);
    __store_i32(state, 0);
    __store_i32(state + 4 as usize, 0);
    return Ok(Muxer { handle: handle, state: state, scratch: __alloc(24), max_packet_bytes: maximum });
  }
}

pub function (muxer: Muxer) state(): State {
  unsafe {
    return match (__load_i32(muxer.state)) { 0 => Configuring, 1 => Started, _ => Stopped };
  }
}

pub function (muxer: Muxer) add_track(format: video.CodecFormat): Result[i32, frame.VideoError] {
  if (muxer.state() != Configuring || format.handle == 0 as usize) {
    return Err(frame.State);
  }
  let track = c_add(muxer.handle, format.handle);
  if (track < 0i64 || track > 2147483646i64) {
    return Err(frame.Platform);
  }
  unsafe {
    let count = __load_i32(muxer.state + 4 as usize);
    if (track as i32 + 1 > count) {
      __store_i32(muxer.state + 4 as usize, track as i32 + 1);
    }
  }
  return Ok(track as i32);
}

pub function (muxer: Muxer) orientation(degrees: i32): Result[void, frame.VideoError] {
  if (muxer.state() != Configuring) {
    return Err(frame.State);
  }
  if (degrees != 0 && degrees != 90 && degrees != 180 && degrees != 270) {
    return Err(frame.Header);
  }
  if (c_orientation(muxer.handle, degrees) != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function (muxer: Muxer) start(): Result[void, frame.VideoError] {
  unsafe {
    if (muxer.state() != Configuring || __load_i32(muxer.state + 4 as usize) == 0) {
      return Err(frame.State);
    }
    if (c_start(muxer.handle) != 0) {
      return Err(frame.Platform);
    }
    __store_i32(muxer.state, 1);
  }
  return Ok(());
}

function valid_sample(track: i32, view: video.BufferView, presentation_time_us: i64, flags: u32): boolean {
  if (track < 0 || view.size < 0 || presentation_time_us < 0i64 || (flags & 4294967290u32) != 0u32 || view.size > 0 && view.data == 0 as usize) {
    return false;
  }
  return true;
}

pub function (muxer: Muxer) write_view(track: i32, view: video.BufferView, presentation_time_us: i64, flags: u32): Result[void, frame.VideoError] {
  if (muxer.state() != Started) {
    return Err(frame.State);
  }
  if (!valid_sample(track, view, presentation_time_us, flags)) {
    return Err(frame.Header);
  }
  if (view.size > muxer.max_packet_bytes) {
    return Err(frame.Limit);
  }
  unsafe {
    if (track >= __load_i32(muxer.state + 4 as usize)) {
      return Err(frame.Header);
    }
    if (view.size == 0) {
      return Ok(());
    }
    __store_i32(muxer.scratch, 0);
    __store_i32(muxer.scratch + 4 as usize, view.size);
    __store_i64(muxer.scratch + 8 as usize, presentation_time_us);
    __store_i32(muxer.scratch + 16 as usize, (flags & video.FLAG_KEY_FRAME) as i32);
    if (c_write(muxer.handle, track as usize, view.data, muxer.scratch) != 0) {
      return Err(frame.Platform);
    }
  }
  return Ok(());
}

pub function (muxer: Muxer) stop(): Result[void, frame.VideoError] {
  if (muxer.state() != Started) {
    return Err(frame.State);
  }
  let status = c_stop(muxer.handle);
  unsafe {
    __store_i32(muxer.state, 2);
  }
  if (status != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}

pub function (muxer: Muxer) close(): Result[void, frame.VideoError] {
  let stopped = if (muxer.state() == Started) { c_stop(muxer.handle) } else { 0 };
  let deleted = c_delete(muxer.handle);
  unsafe {
    __free(muxer.scratch, 24);
    __free(muxer.state, 8);
  }
  if (stopped != 0 || deleted != 0) {
    return Err(frame.Platform);
  }
  return Ok(());
}
```
